package filter

import (
	"sort"

	"google.golang.org/protobuf/proto"

	paginationV1 "go-wind-admin/pkg/localdeps/go-crud/api/gen/go/pagination/v1"
)

// NormalizeFilterExpr 递归规范化过滤表达式，使语义等价的表达式得到逐字节一致的编码。
//
// 背景：QueryStringConverter.parseBaseCondition 用 `for k, v := range conditionMap`
// 遍历 Go map（顺序随机）后按遍历顺序 append repeated Conditions，而 protobuf 的
// deterministic 编码不会重排 repeated 字段。于是同一段 query 两次解析可能得到不同的
// Conditions 排列，导致游标指纹（CursorFilterKey）不稳定：原样回传服务端生成的
// next_cursor 会被误判为篡改而返回 400。
//
// 规范化在 AND/OR 组内对 Conditions 与 Groups 按确定性编码排序。AND/OR 均为幂等
// 交换运算，组内顺序不改变语义，因此排序是安全的。必须递归处理 Groups，
// 否则嵌套 $and/$or 的指纹仍可能不稳定。
func NormalizeFilterExpr(expr *paginationV1.FilterExpr) {
	if expr == nil {
		return
	}

	for _, group := range expr.Groups {
		NormalizeFilterExpr(group)
	}

	sort.SliceStable(expr.Conditions, func(i, j int) bool {
		return stableMessageKey(expr.Conditions[i]) < stableMessageKey(expr.Conditions[j])
	})
	sort.SliceStable(expr.Groups, func(i, j int) bool {
		return stableMessageKey(expr.Groups[i]) < stableMessageKey(expr.Groups[j])
	})
}

// stableMessageKey 计算消息的确定性编码，作为排序键。
// 编码失败返回空串（不改变排序稳定性，仅可能让个别条件排到最前）。
func stableMessageKey(m proto.Message) string {
	if m == nil {
		return ""
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(m)
	if err != nil {
		return ""
	}
	return string(raw)
}
