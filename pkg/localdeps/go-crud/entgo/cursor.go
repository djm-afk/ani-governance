package entgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"go-wind-admin/pkg/localdeps/go-utils/fieldmaskutil"
	"go-wind-admin/pkg/localdeps/go-utils/stringcase"
	"go-wind-admin/pkg/localdeps/go-wind/log"

	paginationV1 "go-wind-admin/pkg/localdeps/go-crud/api/gen/go/pagination/v1"
	"go-wind-admin/pkg/localdeps/go-crud/entgo/ent"
	paginationCurd "go-wind-admin/pkg/localdeps/go-crud/pagination"
	paginationFilter "go-wind-admin/pkg/localdeps/go-crud/pagination/filter"
)

// ErrInvalidCursorRequest 表示调用方给出的游标分页参数不合法：limit 越界、游标被篡改，
// 或游标与本次请求的资源/租户上下文/筛选/排序不匹配。调用方应把它映射为 400，
// 而不是当作服务端故障（500）或静默降级为首页。
var ErrInvalidCursorRequest = errors.New("invalid cursor pagination request")

// CursorSortKind 描述排序列在游标里的取值类型，用于在解码游标时把规范字符串
// 还原成数据库驱动能正确比较的驱动值（时间列传 string 会让 PG 报
// "operator does not exist: timestamp with time zone > text"）。
type CursorSortKind int

const (
	// CursorSortString 字符串列（含以字符串存储的枚举列）。
	CursorSortString CursorSortKind = iota
	// CursorSortUint 无符号整型列。
	CursorSortUint
	// CursorSortTime 时间列。
	CursorSortTime
)

// CursorSpec 描述一次游标分页的资源绑定与排序方式。
// 它由业务仓储提供：通用仓库不知道实体的字段语义，因此排序值取值与唯一 ID
// 取值通过回调下沉到业务侧，其余（参数校验、游标编解码、键集谓词、下一页判断、
// 计数）都集中在本包的 ListWithCursor 里。
//
// 回调一律作用在 ENTITY（数据库实体）而不是 DTO 上：可空列在实体里是指针
// （*string / *time.Time），能如实区分 SQL NULL 与空值；经 DTO 映射后两者都会
// 退化成零值，键集条件会因此漏掉 NULL 区段的行。
type CursorSpec[ENTITY any] struct {
	// Resource 资源标识，如 "identity.user"：游标不可跨资源复用。
	Resource string
	// Context 上下文类型："tenant"、"platform" 或 "system"。
	Context string
	// TenantID 可信租户 ID（取自服务端 viewer，不信任入站参数）；平台/系统上下文为 0。
	TenantID uint32
	// FilterKey 筛选 + 排序的规范化指纹，见 CursorFilterKey。
	FilterKey string
	// SortColumn 主排序列（数据库列名）。
	SortColumn string
	// Desc 主排序方向是否为降序；唯一 ID 兜底列跟随同一方向。
	Desc bool
	// SortKind 主排序列的取值类型。
	SortKind CursorSortKind
	// IDColumn 唯一 ID 兜底列，空值按 "id" 处理。
	IDColumn string
	// ExtractID 从实体取出唯一 ID。
	ExtractID func(entity *ENTITY) uint64
	// ExtractSortValue 从实体取出排序值并格式化为游标内的规范字符串
	// （时间用 FormatCursorTime，整型用十进制，字符串原样）；第二个返回值为 true
	// 表示该列的值为 SQL NULL。
	ExtractSortValue func(entity *ENTITY) (string, bool)
	// DeferHelperTrim 为 true 时不在此处清理"仅为生成游标而多读"的辅助列
	// （排序列与唯一 ID），而是把它们经 PagingResult.HelperPaths 返回给调用方，
	// 由调用方在完成关联回填等依赖 ID 的收尾工作后再调用 TrimCursorHelperFields。
	// 用途：fieldMask 未包含 id 时，关联回填仍需要实体 ID（见 user_cursor.go）。
	DeferHelperTrim bool
}

// FormatCursorTime 把时间格式化为游标内的规范排序值（UTC、RFC3339Nano）。
// 纳秒精度可完整往返，因此键集比较不会因精度丢失而重复或漏行。
func FormatCursorTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// CursorFilterKey 计算游标绑定的"筛选 + 排序"指纹。
//
// 它必须在业务仓储增强请求（例如把 role_id 之类的关联过滤展开成 id IN (...)）之前
// 调用，绑定的是调用方原始筛选条件，这样同一筛选下的多次翻页得到同一指纹，
// 而切换筛选或排序会使旧游标失效（返回 400），避免被静默套用到另一组结果集上。
func CursorFilterKey(req *paginationV1.PagingRequest, sortColumn string, desc bool) string {
	if req == nil {
		return ""
	}

	h := sha256.New()
	if filterExpr, err := paginationFilter.ConvertFilterByPagingRequest(req); err == nil && filterExpr != nil {
		// 规范化必须在 marshal 之前：QueryStringConverter 按 Go map 的随机遍历顺序
		// 追加 Conditions，而 deterministic 编码不重排 repeated 字段。不规范化时，
		// 语义相同的两条 query 可能得到不同指纹，使原样回传的 next_cursor 被误判为篡改。
		// 规范化作用在副本上，避免就地改动调用方（applyUserListFilters 等）持有的表达式。
		normalized := proto.Clone(filterExpr).(*paginationV1.FilterExpr)
		paginationFilter.NormalizeFilterExpr(normalized)
		if raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(normalized); err == nil {
			h.Write(raw)
		}
	} else {
		// 过滤表达式解析失败时按原始字符串绑定：同一请求仍得到同一指纹，
		// 具体报错由后续查询路径统一处理。
		h.Write([]byte(req.GetQuery()))
		h.Write([]byte{0})
		h.Write([]byte(req.GetFilter()))
	}
	h.Write([]byte{0})
	h.Write([]byte("sort:" + sortColumn + ":" + strconv.FormatBool(desc)))
	return hex.EncodeToString(h.Sum(nil))
}

// ListWithCursor 用游标（键集）方式查询一页列表。
//
// 语义要点（与 page/offset 分页的差异）：
//   - limit 未传取 CursorDefaultLimit，超出 [1, CursorMaxLimit] 直接报错而不截断；
//   - 排序在业务排序列之后强制追加唯一 ID，保证同值排序时翻页不重不漏；
//   - 读取 limit+1 条，只有确实多出一条时才返回 next_cursor（末页为空）；
//   - total 与 list 使用同一组筛选条件（含租户/权限过滤），但不含键集窗口，
//     因此它是不受游标影响的"当前条件下的总数"；
//   - fieldMask 只裁剪输出：为生成游标额外读取的排序列与唯一 ID 会在输出前清理。
func (r *Repository[
	ENT_QUERY, ENT_SELECT,
	ENT_CREATE, ENT_CREATE_BULK,
	ENT_UPDATE, ENT_UPDATE_ONE,
	ENT_DELETE,
	PREDICATE, DTO, ENTITY,
]) ListWithCursor(
	ctx context.Context,
	builder ListBuilder[ENT_QUERY, ENT_SELECT, ENTITY],
	countBuilder ListBuilder[ENT_QUERY, ENT_SELECT, ENTITY],
	req *paginationV1.PagingRequest,
	spec CursorSpec[ENTITY],
) (*PagingResult[DTO], error) {
	if req == nil {
		return nil, errors.New("pagination request is nil")
	}
	if builder == nil {
		return nil, errors.New("query builder is nil")
	}
	if spec.ExtractID == nil || spec.ExtractSortValue == nil {
		return nil, errors.New("cursor spec is incomplete: extractors are required")
	}
	if spec.Resource == "" || spec.Context == "" {
		return nil, errors.New("cursor spec is incomplete: cursor binding is required")
	}
	if spec.IDColumn == "" {
		spec.IDColumn = "id"
	}
	if !ent.IsValidFieldName(spec.SortColumn) || !ent.IsValidFieldName(spec.IDColumn) {
		return nil, fmt.Errorf("%w: invalid sort column %q", ErrInvalidCursorRequest, spec.SortColumn)
	}

	limit, err := paginationCurd.ResolveCursorLimit(req.Limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCursorRequest, err)
	}

	binding := paginationCurd.CursorBinding{
		Resource:  spec.Resource,
		Context:   spec.Context,
		TenantID:  spec.TenantID,
		FilterKey: spec.FilterKey,
	}

	var cursor *paginationCurd.Cursor
	if token := req.GetCursor(); token != "" {
		cursor, err = paginationCurd.DecodeCursor(token, binding, paginationCurd.CursorSecret())
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCursorRequest, err)
		}
	}

	// 过滤条件：与分页窗口无关，list 与 count 共用同一组条件。
	filterExpr, filterErr := paginationFilter.ConvertFilterByPagingRequest(req)
	if filterErr != nil {
		log.Error(context.Background(), fmt.Sprintf("convert filter by pagination request failed: %s", filterErr.Error()))
	}
	whereSelectors, filterErr := r.structuredFilter.BuildSelectors(filterExpr)
	if filterErr != nil {
		log.Error(context.Background(), fmt.Sprintf("build structured filter selectors failed: %s", filterErr.Error()))
	}

	querySelectors := make([]func(s *sql.Selector), 0, 5)
	querySelectors = append(querySelectors, whereSelectors...)

	// SELECT 裁剪：游标需要排序列与唯一 ID，即便 fieldMask 未包含它们也要在内部读取，
	// 输出前再按原始 mask 裁掉（见下方 trimCursorHelperFields）。
	var helperPaths []string
	if fm := req.GetFieldMask(); fm != nil && len(fm.GetPaths()) > 0 {
		paths := make([]string, 0, len(fm.GetPaths())+2)
		paths = append(paths, fm.GetPaths()...)
		for _, helper := range []string{spec.SortColumn, spec.IDColumn} {
			if containsCursorPath(paths, helper) {
				continue
			}
			paths = append(paths, helper)
			helperPaths = append(helperPaths, helper)
		}
		selectSelector, selectErr := r.fieldSelector.BuildSelector(paths)
		if selectErr != nil {
			log.Error(context.Background(), fmt.Sprintf("build field select selector failed: %s", selectErr.Error()))
		} else if selectSelector != nil {
			querySelectors = append(querySelectors, selectSelector)
		}
	}

	// 稳定排序：客户端排序列 + 唯一 ID 兜底。
	// 主排序列可空，NULLS 位置必须显式声明（PostgreSQL 与 SQLite 的默认 NULL 位置
	// 相反），并与下方键集谓词的 NULL 分支保持一致：降序 NULL 在前、升序 NULL 在后。
	querySelectors = append(querySelectors, func(s *sql.Selector) {
		if spec.SortColumn == spec.IDColumn {
			if spec.Desc {
				s.OrderBy(sql.Desc(s.C(spec.IDColumn)))
			} else {
				s.OrderBy(sql.Asc(s.C(spec.IDColumn)))
			}
			return
		}
		if spec.Desc {
			s.OrderBy(s.C(spec.SortColumn) + " DESC NULLS FIRST")
			s.OrderBy(sql.Desc(s.C(spec.IDColumn)))
		} else {
			s.OrderBy(s.C(spec.SortColumn) + " ASC NULLS LAST")
			s.OrderBy(sql.Asc(s.C(spec.IDColumn)))
		}
	})

	if cursor != nil {
		keysetSelector, keysetErr := buildCursorKeysetSelector(spec, cursor)
		if keysetErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCursorRequest, keysetErr)
		}
		querySelectors = append(querySelectors, keysetSelector)
	}

	// 多取一条：只有确实存在下一条时才生成 next_cursor。
	querySelectors = append(querySelectors, func(s *sql.Selector) { s.Limit(limit + 1) })

	builder.Modify(querySelectors...)

	entities, err := builder.All(ctx)
	if err != nil {
		log.Error(context.Background(), fmt.Sprintf("query list failed: %s", err.Error()))
		return nil, errors.New("query list failed")
	}

	hasNext := len(entities) > limit
	if hasNext {
		entities = entities[:limit]
	}

	dtos := make([]*DTO, 0, len(entities))
	for _, entity := range entities {
		dtos = append(dtos, r.mapper.ToDTO(entity))
	}

	var nextCursor string
	if hasNext && len(entities) > 0 {
		// 游标取自实体而非 DTO：可空列的 NULL 与空值只有在实体上才可区分。
		last := entities[len(entities)-1]
		sortValue, isNull := spec.ExtractSortValue(last)
		var sortKey *string
		if !isNull {
			sortKey = &sortValue
		}
		nextCursor, err = paginationCurd.EncodeCursor(binding, paginationCurd.Cursor{
			SortKey: sortKey,
			ID:      spec.ExtractID(last),
		}, paginationCurd.CursorSecret())
		if err != nil {
			log.Error(context.Background(), fmt.Sprintf("encode pagination cursor failed: %s", err.Error()))
			return nil, errors.New("encode pagination cursor failed")
		}
	}

	if len(helperPaths) > 0 && !spec.DeferHelperTrim {
		TrimCursorHelperFields(dtos, helperPaths)
	}

	var count int
	if countBuilder != nil {
		if len(whereSelectors) != 0 {
			countBuilder.Modify(whereSelectors...)
		}
		if count, err = countBuilder.Count(ctx); err != nil {
			log.Error(context.Background(), fmt.Sprintf("query count failed: %s", err.Error()))
			return nil, errors.New("query count failed")
		}
	}

	return &PagingResult[DTO]{
		Items:       dtos,
		Total:       uint64(count),
		NextCursor:  nextCursor,
		HelperPaths: deferHelperPaths(spec, helperPaths),
	}, nil
}

// deferHelperPaths 仅在调用方要求延迟裁剪时回传辅助列清单。
func deferHelperPaths[ENTITY any](spec CursorSpec[ENTITY], helperPaths []string) []string {
	if !spec.DeferHelperTrim {
		return nil
	}
	return helperPaths
}

// buildCursorKeysetSelector 构造 (排序列, 唯一 ID) 的键集谓词。
//
// 排序由本包显式声明 NULLS 位置（降序 NULL 在前、升序 NULL 在后），因此四种情形
// 都必须显式覆盖，否则跨页会漏行：
//
//	降序（NULL 在前，游标排序值为非 NULL）：(k < lastK) OR (k = lastK AND id < lastID)
//	降序（NULL 在前，游标排序值为 NULL）  ：(k IS NULL AND id < lastID) OR k IS NOT NULL
//	升序（NULL 在后，游标排序值为非 NULL）：(k > lastK) OR (k = lastK AND id > lastID) OR k IS NULL
//	升序（NULL 在后，游标排序值为 NULL）  ：(k IS NULL AND id > lastID)
//
// 主排序列即唯一 ID（非空）时退化为单纯的 id 比较。
func buildCursorKeysetSelector[ENTITY any](spec CursorSpec[ENTITY], cursor *paginationCurd.Cursor) (func(s *sql.Selector), error) {
	if spec.SortColumn == spec.IDColumn {
		return func(s *sql.Selector) {
			if spec.Desc {
				s.Where(sql.LT(s.C(spec.IDColumn), cursor.ID))
			} else {
				s.Where(sql.GT(s.C(spec.IDColumn), cursor.ID))
			}
		}, nil
	}

	sortValue, isNull, err := parseCursorSortValue(cursor.SortKey, spec.SortKind)
	if err != nil {
		return nil, err
	}

	return func(s *sql.Selector) {
		sortColumn, idColumn := s.C(spec.SortColumn), s.C(spec.IDColumn)

		if isNull {
			// 游标落在 NULL 区段内，只能按唯一 ID 在 NULL 区段里继续推进；
			// 降序时 NULL 区段之后还有全部非 NULL 行，必须一并放行。
			if spec.Desc {
				s.Where(sql.Or(
					sql.And(sql.IsNull(sortColumn), sql.LT(idColumn, cursor.ID)),
					sql.NotNull(sortColumn),
				))
				return
			}
			s.Where(sql.And(sql.IsNull(sortColumn), sql.GT(idColumn, cursor.ID)))
			return
		}

		if spec.Desc {
			s.Where(sql.Or(
				sql.LT(sortColumn, sortValue),
				sql.And(sql.EQ(sortColumn, sortValue), sql.LT(idColumn, cursor.ID)),
			))
			return
		}
		// 升序时 NULL 全部排在非 NULL 之后，因此可以无条件包含 NULL 行。
		s.Where(sql.Or(
			sql.GT(sortColumn, sortValue),
			sql.And(sql.EQ(sortColumn, sortValue), sql.GT(idColumn, cursor.ID)),
			sql.IsNull(sortColumn),
		))
	}, nil
}

// parseCursorSortValue 把游标内的规范排序值还原成驱动值。
// 返回 (驱动值, 是否为 SQL NULL, 错误)；sortKey 为 nil 即该行的排序值是 NULL。
func parseCursorSortValue(sortKey *string, kind CursorSortKind) (any, bool, error) {
	if sortKey == nil {
		return nil, true, nil
	}

	switch kind {
	case CursorSortString:
		return *sortKey, false, nil
	case CursorSortUint:
		value, err := strconv.ParseUint(*sortKey, 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("cursor sort value %q is not an unsigned integer", *sortKey)
		}
		return value, false, nil
	case CursorSortTime:
		value, err := time.Parse(time.RFC3339Nano, *sortKey)
		if err != nil {
			return nil, false, fmt.Errorf("cursor sort value %q is not an RFC3339 timestamp", *sortKey)
		}
		// 交给驱动时换成本地时区：PostgreSQL 按绝对时刻比较，时区不改变语义；
		// SQLite 夹具驱动把 time.Time 列存成 Go 的时间字符串并按整串文本比较，
		// 参数写成同一时区至少能让"先后"比较与库内取值落在同一空间。
		// 注意 SQLite 下同值判定仍不可靠（库内文本带 m=+... 单调时钟后缀，参数无法复现），
		// 因此时间列的键集翻页只在 PostgreSQL 上成立，SQLite 只用于其它维度的用例。
		return value.Local(), false, nil
	default:
		return nil, false, fmt.Errorf("unsupported cursor sort kind %d", int(kind))
	}
}

// TrimCursorHelperFields 把"仅为生成游标而多读"的列从响应中清除，保持
// fieldMask 只裁剪输出的语义。清除失败（路径对 DTO 不合法等）只记录日志：
// 多出的一个字段不影响契约，不值得让整个请求失败。
//
// 当 CursorSpec.DeferHelperTrim 为 true 时，ListWithCursor 不自行调用它，
// 而是把待清理路径经 PagingResult.HelperPaths 交给业务调用方——后者需在
// 关联回填（依赖实体 ID）完成后再调用本函数。
func TrimCursorHelperFields[DTO any](dtos []*DTO, paths []string) {
	mask := &fieldmaskpb.FieldMask{Paths: paths}
	for _, dto := range dtos {
		if dto == nil {
			continue
		}
		msg, ok := any(dto).(proto.Message)
		if !ok {
			continue
		}
		if err := fieldmaskutil.PruneByFieldMask(&msg, mask); err != nil {
			log.Warn(context.Background(), fmt.Sprintf("prune cursor helper fields failed: %s", err.Error()))
		}
	}
}

// containsCursorPath 判断 fieldMask 路径里是否已包含指定列（按 snake_case 归一化比较，
// 因为 fieldMask 允许 camelCase 拼写）。
func containsCursorPath(paths []string, column string) bool {
	target := stringcase.ToSnakeCase(column)
	for _, path := range paths {
		if stringcase.ToSnakeCase(strings.TrimSpace(path)) == target {
			return true
		}
	}
	return false
}
