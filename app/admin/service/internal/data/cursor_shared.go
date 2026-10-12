package data

import (
	"strings"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	paginationV1 "go-wind-admin/pkg/localdeps/go-crud/api/gen/go/pagination/v1"
	paginationCurd "go-wind-admin/pkg/localdeps/go-crud/pagination"
	"go-wind-admin/pkg/localdeps/go-crud/pagination/sorting"
	"go-wind-admin/pkg/localdeps/go-utils/stringcase"
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

// withFieldMaskIncludingID 返回一个确保 fieldMask 包含 id 的请求副本。
//
// 当原 fieldMask 为空（即 SELECT *）或已含 id 时原样返回；否则克隆请求并把 id 补入路径。
//
// 用途：api-keys 的输出需把内部 ON/OFF 映射为公开布尔 is_active，实现上是
// "先取一页的 ID，再按 ID 二次查询实体"。fieldMask 会被当作 SELECT 字段掩码，
// 若其中不含 id，取页结果的 ID 便是零值，二次查询 IDIn(0,0,...) 命不中任何行、
// 整页 items 变空。id 是主键、恒非空，且与游标分页内部"为排序/回填多读唯一 ID"
// 的处理一致，故显式补入，保证取页始终拿得到 ID。
func withFieldMaskIncludingID(req *paginationV1.PagingRequest) *paginationV1.PagingRequest {
	fm := req.GetFieldMask()
	if fm == nil || len(fm.GetPaths()) == 0 {
		return req
	}
	for _, path := range fm.GetPaths() {
		if stringcase.ToSnakeCase(strings.TrimSpace(path)) == "id" {
			return req
		}
	}

	cloned := proto.Clone(req).(*paginationV1.PagingRequest)
	paths := make([]string, 0, len(fm.GetPaths())+1)
	paths = append(paths, fm.GetPaths()...)
	paths = append(paths, "id")
	cloned.FieldMask = &fieldmaskpb.FieldMask{Paths: paths}
	return cloned
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
