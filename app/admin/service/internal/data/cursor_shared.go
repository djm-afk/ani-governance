package data

import (
	kerrors "github.com/go-kratos/kratos/v2/errors"

	paginationV1 "go-wind-admin/pkg/localdeps/go-crud/api/gen/go/pagination/v1"
	paginationCurd "go-wind-admin/pkg/localdeps/go-crud/pagination"
	"go-wind-admin/pkg/localdeps/go-crud/pagination/sorting"
)

// cursorBadRequest 是各领域 ErrorBadRequest 的统一签名（返回 kratos Error）。
type cursorBadRequest func(format string, args ...any) *kerrors.Error

// resolveCursorSortColumn 解析游标分页的单一排序列与方向。
//
// 共用逻辑：优先取 req.Sorting；为空时解析 req.OrderBy；仍未指定则用默认列。
// 只支持单一排序列（多列无法用单列键集表达），超过一个即报错；
// 排序列不在 allowed 内一律报错（宁可明确拒绝，也不返回顺序与游标窗口不一致的数据）。
// badRequest 用于按调用方领域构造 400 错误。
func resolveCursorSortColumn(
	req *paginationV1.PagingRequest,
	defaultColumn string,
	allowed map[string]struct{},
	badRequest cursorBadRequest,
) (column string, desc bool, err error) {
	sortings := req.GetSorting()
	if len(sortings) == 0 {
		if orderBy := req.GetOrderBy(); orderBy != "" {
			converted, convErr := sorting.NewOrderByStringConverter().Convert(orderBy)
			if convErr != nil {
				return "", false, badRequest("invalid orderBy: %s", convErr.Error())
			}
			sortings = converted
		}
	}

	effective := make([]*paginationV1.Sorting, 0, len(sortings))
	for _, s := range sortings {
		if s == nil || s.GetField() == "" {
			continue
		}
		effective = append(effective, s)
	}
	if len(effective) > 1 {
		return "", false, badRequest("cursor pagination supports a single sort field, got %d", len(effective))
	}

	column, desc = defaultColumn, true
	if len(effective) == 1 {
		column = effective[0].GetField()
		desc = effective[0].GetDirection() == paginationV1.Sorting_DESC
	}

	if _, ok := allowed[column]; !ok {
		return "", false, badRequest("unsupported sort field for cursor pagination: %s", column)
	}
	return column, desc, nil
}

// hasCursorWindowParams 表请求是否携带任一非游标分页窗口参数。
// 用指针字段判定（而非值非零），与 users 既有实现一致：显式传 page=0 也算窗口参数。
func hasCursorWindowParams(req *paginationV1.PagingRequest) bool {
	return req.Page != nil || req.PageSize != nil || req.Offset != nil || req.Token != nil
}

// checkCursorWindowConflict 校验 cursor 与其它分页方式（page/pageSize/offset/token
// 窗口参数，以及 noPaging 开关）互斥。
// 该检查不受 noPaging 影响：noPaging 与 cursor 同时出现同样应拒绝
// （否则客户端以为在翻页、服务端却忽略了游标）。
func checkCursorWindowConflict(req *paginationV1.PagingRequest, badRequest cursorBadRequest) error {
	if req.GetCursor() != "" && (hasCursorWindowParams(req) || req.GetNoPaging()) {
		return badRequest("cursor is mutually exclusive with page/pageSize/offset/token/noPaging")
	}
	return nil
}

// shouldUseCursorPaging 判定当前请求是否走游标分页：
// 非 noPaging 且（带 cursor 或不带任何窗口参数）。与 users 的默认行为一致。
func shouldUseCursorPaging(req *paginationV1.PagingRequest) bool {
	if req.GetNoPaging() {
		return false
	}
	return req.GetCursor() != "" || !hasCursorWindowParams(req)
}

// validateCursorRequest 前置校验游标分页参数：limit 越界、游标被篡改或与绑定
// （资源/上下文/租户/筛选/排序）不匹配时返回 400。与 ListWithCursor 内部校验同源，
// 使校验可在"空集短路"等分支之前先行 fail-closed。
func validateCursorRequest(
	req *paginationV1.PagingRequest,
	resource, bindingContext string,
	tenantID uint32,
	filterKey string,
	badRequest cursorBadRequest,
) error {
	if _, err := paginationCurd.ResolveCursorLimit(req.Limit); err != nil {
		return badRequest("%s", err.Error())
	}
	if token := req.GetCursor(); token != "" {
		binding := paginationCurd.CursorBinding{
			Resource:  resource,
			Context:   bindingContext,
			TenantID:  tenantID,
			FilterKey: filterKey,
		}
		if _, err := paginationCurd.DecodeCursor(token, binding, paginationCurd.CursorSecret()); err != nil {
			return badRequest("%s", err.Error())
		}
	}
	return nil
}
