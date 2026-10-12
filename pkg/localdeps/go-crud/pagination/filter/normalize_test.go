package filter

import (
	"testing"

	"google.golang.org/protobuf/proto"

	paginationV1 "go-wind-admin/pkg/localdeps/go-crud/api/gen/go/pagination/v1"
)

// normalizedBytes 规范化后取确定性编码，用于比较两个语义等价表达式是否逐字节一致。
func normalizedBytes(t *testing.T, expr *paginationV1.FilterExpr) []byte {
	t.Helper()
	NormalizeFilterExpr(expr)
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(expr)
	if err != nil {
		t.Fatalf("marshal normalized expr failed: %v", err)
	}
	return raw
}

func eqCondition(field, value string) *paginationV1.FilterCondition {
	return &paginationV1.FilterCondition{
		Field:      field,
		Op:         paginationV1.Operator_EQ,
		ValueOneof: &paginationV1.FilterCondition_Value{Value: value},
	}
}

// TestNormalizeFilterExpr_StabilizesConditionOrder 验证 §3.1：
// 同一组 AND 条件以不同顺序出现时，规范化后得到逐字节一致的编码。
// QueryStringConverter 用 Go map 的随机遍历顺序 append Conditions，
// 不规范化会让语义相同的 query 产生不同指纹，使回传的 next_cursor 被误判为篡改。
func TestNormalizeFilterExpr_StabilizesConditionOrder(t *testing.T) {
	a := &paginationV1.FilterExpr{
		Type: paginationV1.ExprType_AND,
		Conditions: []*paginationV1.FilterCondition{
			eqCondition("username", "alice"),
			eqCondition("nickname", "小明"),
			eqCondition("status", "NORMAL"),
		},
	}
	b := &paginationV1.FilterExpr{
		Type: paginationV1.ExprType_AND,
		Conditions: []*paginationV1.FilterCondition{
			eqCondition("status", "NORMAL"),
			eqCondition("username", "alice"),
			eqCondition("nickname", "小明"),
		},
	}

	got, want := normalizedBytes(t, a), normalizedBytes(t, b)
	if string(got) != string(want) {
		t.Fatalf("语义等价的 AND 条件规范化后编码不一致:\n a=%q\n b=%q", got, want)
	}
}

// TestNormalizeFilterExpr_RecursesIntoGroups 验证 §3.1 的递归要求：
// 嵌套 $and/$or 的子组内部条件顺序不同，也必须归一到同一编码。
func TestNormalizeFilterExpr_RecursesIntoGroups(t *testing.T) {
	groupA := &paginationV1.FilterExpr{
		Type: paginationV1.ExprType_OR,
		Conditions: []*paginationV1.FilterCondition{
			eqCondition("x", "1"),
			eqCondition("y", "2"),
		},
	}
	groupB := &paginationV1.FilterExpr{
		Type: paginationV1.ExprType_OR,
		Conditions: []*paginationV1.FilterCondition{
			eqCondition("y", "2"),
			eqCondition("x", "1"),
		},
	}
	a := &paginationV1.FilterExpr{Type: paginationV1.ExprType_AND, Groups: []*paginationV1.FilterExpr{groupA}}
	b := &paginationV1.FilterExpr{Type: paginationV1.ExprType_AND, Groups: []*paginationV1.FilterExpr{groupB}}

	gotA, gotB := normalizedBytes(t, a), normalizedBytes(t, b)
	if string(gotA) != string(gotB) {
		t.Fatalf("嵌套子组条件规范化后编码不一致:\n a=%q\n b=%q", gotA, gotB)
	}
}

// TestNormalizeFilterExpr_DoesNotDropConditions 验证规范化只重排顺序、不改语义：
// 条件数量与集合必须保持。
func TestNormalizeFilterExpr_DoesNotDropConditions(t *testing.T) {
	expr := &paginationV1.FilterExpr{
		Type: paginationV1.ExprType_AND,
		Conditions: []*paginationV1.FilterCondition{
			eqCondition("c", "3"),
			eqCondition("a", "1"),
			eqCondition("b", "2"),
		},
	}
	NormalizeFilterExpr(expr)
	if len(expr.GetConditions()) != 3 {
		t.Fatalf("规范化不得增删条件，期望 3 条，实际 %d 条", len(expr.GetConditions()))
	}
	seen := map[string]string{}
	for _, cond := range expr.GetConditions() {
		seen[cond.GetField()] = cond.GetValue()
	}
	for field, value := range map[string]string{"a": "1", "b": "2", "c": "3"} {
		if seen[field] != value {
			t.Fatalf("规范化改变了条件语义: field=%s 期望 %q 实际 %q", field, value, seen[field])
		}
	}
}
