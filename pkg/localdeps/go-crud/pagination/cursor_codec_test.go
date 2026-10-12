package pagination

import (
	"errors"
	"strings"
	"testing"
)

const testCursorSecret = "cursor-secret-32-bytes-long-xxxx"

func testBinding() CursorBinding {
	return CursorBinding{
		Resource:  "identity.user",
		Context:   "tenant",
		TenantID:  7,
		FilterKey: "abcdef0123456789",
	}
}

// cursorString 是构造非 NULL 排序值的便捷函数。
func cursorString(v string) *string { return &v }

// TestCursorCodec_RoundTrip 验证绑定信息与游标内容的编码-解码往返，
// 且 NULL 排序值与空字符串排序值必须可区分（键集条件要据此穿过 NULL 区段）。
func TestCursorCodec_RoundTrip(t *testing.T) {
	binding := testBinding()

	cases := map[string]*string{
		"value":        cursorString("1024"),
		"empty string": cursorString(""),
		"null":         nil,
	}
	for name, sortKey := range cases {
		token, err := EncodeCursor(binding, Cursor{SortKey: sortKey, ID: 1024}, []byte(testCursorSecret))
		if err != nil {
			t.Fatalf("%s: EncodeCursor failed: %v", name, err)
		}
		if !strings.HasPrefix(token, signedCursorPrefix) {
			t.Fatalf("expected %q prefix, got %q", signedCursorPrefix, token)
		}

		got, err := DecodeCursor(token, binding, []byte(testCursorSecret))
		if err != nil {
			t.Fatalf("%s: DecodeCursor failed: %v", name, err)
		}
		switch {
		case sortKey == nil && got.SortKey != nil:
			t.Fatalf("%s: NULL sort value must round trip as NULL, got %q", name, *got.SortKey)
		case sortKey != nil && got.SortKey == nil:
			t.Fatalf("%s: non-NULL sort value must not become NULL", name)
		case sortKey != nil && *got.SortKey != *sortKey:
			t.Fatalf("%s: round trip mismatch: got %q, want %q", name, *got.SortKey, *sortKey)
		}
		if got.ID != 1024 {
			t.Fatalf("%s: id round trip mismatch: %d", name, got.ID)
		}
	}
}

// TestCursorCodec_TimeSortKeyIsOpaque 验证游标对客户端不透明：
// 排序值与租户等绑定信息不以明文出现在游标里。
func TestCursorCodec_TimeSortKeyIsOpaque(t *testing.T) {
	binding := testBinding()
	token, err := EncodeCursor(binding, Cursor{SortKey: cursorString("2026-10-01T12:00:00Z"), ID: 3}, []byte(testCursorSecret))
	if err != nil {
		t.Fatalf("EncodeCursor failed: %v", err)
	}
	for _, plain := range []string{"2026-10-01T12:00:00Z", "identity.user", "abcdef0123456789"} {
		if strings.Contains(token, plain) {
			t.Fatalf("cursor leaks plaintext %q: %s", plain, token)
		}
	}
}

// TestCursorCodec_TamperedRejected 验证载荷被改动后签名校验失败。
func TestCursorCodec_TamperedRejected(t *testing.T) {
	binding := testBinding()
	token, err := EncodeCursor(binding, Cursor{SortKey: cursorString("100"), ID: 100}, []byte(testCursorSecret))
	if err != nil {
		t.Fatalf("EncodeCursor failed: %v", err)
	}

	body := strings.TrimPrefix(token, signedCursorPrefix)
	dot := strings.LastIndex(body, ".")
	if dot < 0 {
		t.Fatalf("malformed cursor %q", token)
	}
	tampered := signedCursorPrefix + flip(body[:dot]) + body[dot:]

	if _, err := DecodeCursor(tampered, binding, []byte(testCursorSecret)); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected tampered cursor to be rejected, got %v", err)
	}
}

// TestCursorCodec_WrongSecretRejected 验证用其它密钥解不开游标。
func TestCursorCodec_WrongSecretRejected(t *testing.T) {
	binding := testBinding()
	token, err := EncodeCursor(binding, Cursor{SortKey: cursorString("1"), ID: 1}, []byte(testCursorSecret))
	if err != nil {
		t.Fatalf("EncodeCursor failed: %v", err)
	}
	if _, err := DecodeCursor(token, binding, []byte("another-secret")); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("expected wrong secret to be rejected, got %v", err)
	}
}

// TestCursorCodec_BindingMismatchRejected 验证资源、上下文、租户、筛选指纹
// 任一项不一致都拒绝：游标不可跨资源/跨租户/跨筛选复用。
func TestCursorCodec_BindingMismatchRejected(t *testing.T) {
	binding := testBinding()
	token, err := EncodeCursor(binding, Cursor{SortKey: cursorString("1"), ID: 1}, []byte(testCursorSecret))
	if err != nil {
		t.Fatalf("EncodeCursor failed: %v", err)
	}

	cases := map[string]CursorBinding{
		"resource": {Resource: "identity.tenant", Context: binding.Context, TenantID: binding.TenantID, FilterKey: binding.FilterKey},
		"context":  {Resource: binding.Resource, Context: "platform", TenantID: binding.TenantID, FilterKey: binding.FilterKey},
		"tenant":   {Resource: binding.Resource, Context: binding.Context, TenantID: 8, FilterKey: binding.FilterKey},
		"filter":   {Resource: binding.Resource, Context: binding.Context, TenantID: binding.TenantID, FilterKey: "other"},
	}
	for name, mismatched := range cases {
		if _, err := DecodeCursor(token, mismatched, []byte(testCursorSecret)); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("%s mismatch must be rejected, got %v", name, err)
		}
	}
}

// TestCursorCodec_InvalidInputRejected 验证空串、非 c1. 前缀、缺密钥、超长载荷一律拒绝，
// 不做"降级为首页"处理。
func TestCursorCodec_InvalidInputRejected(t *testing.T) {
	binding := testBinding()
	oversized := signedCursorPrefix + strings.Repeat("A", maxCursorPayloadLen+1) + ".deadbeef"

	cases := map[string]struct {
		token  string
		secret []byte
	}{
		"empty":          {"", []byte(testCursorSecret)},
		"no prefix":      {"v2.abc.def", []byte(testCursorSecret)},
		"legacy token":   {EncodeAndSign(9, []byte(testCursorSecret)), []byte(testCursorSecret)},
		"missing dot":    {signedCursorPrefix + "abcdef", []byte(testCursorSecret)},
		"empty payload":  {signedCursorPrefix + ".deadbeef", []byte(testCursorSecret)},
		"oversized":      {oversized, []byte(testCursorSecret)},
		"missing secret": {signedCursorPrefix + "abcdef.deadbeef", nil},
	}
	for name, tc := range cases {
		if _, err := DecodeCursor(tc.token, binding, tc.secret); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("%s must be rejected, got %v", name, err)
		}
	}

	if _, err := EncodeCursor(binding, Cursor{SortKey: cursorString("1"), ID: 1}, nil); err == nil {
		t.Fatal("EncodeCursor must refuse to sign without a secret")
	}
}

// TestCursorCodec_EncodeRejectsOversizedPayload 验证 §3.4：编码侧与解码侧的长度
// 上限必须一致。排序值过长（如未设置列长度上限的长字符串列）时，Encode 必须明确
// 失败，而不是产出一个必然被 DecodeCursor 拒绝的坏游标——否则客户端原样回传
// next_cursor 反而得到 400。
func TestCursorCodec_EncodeRejectsOversizedPayload(t *testing.T) {
	binding := testBinding()

	// 在长度上限内：正常往返
	fitting := strings.Repeat("a", maxCursorPayloadLen/2)
	if _, err := EncodeCursor(binding, Cursor{SortKey: cursorString(fitting), ID: 1}, []byte(testCursorSecret)); err != nil {
		t.Fatalf("未超限的排序值应可编码，实际 %v", err)
	}

	// 远超上限：编码必须失败，且错误可被 ErrInvalidCursor 识别
	oversized := strings.Repeat("a", maxCursorPayloadLen*2)
	_, err := EncodeCursor(binding, Cursor{SortKey: cursorString(oversized), ID: 1}, []byte(testCursorSecret))
	if err == nil {
		t.Fatal("超长排序值必须让 EncodeCursor 明确失败，不得产出坏游标")
	}
	if !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("超长载荷错误应可被 ErrInvalidCursor 识别，实际 %v", err)
	}
}

// TestCursorCodec_TokenCodecCannotParseCursor 验证两种游标格式互不兼容：
// token 的解码器不能把签名游标当成自己的 token 读出来。
func TestCursorCodec_TokenCodecCannotParseCursor(t *testing.T) {
	binding := testBinding()
	token, err := EncodeCursor(binding, Cursor{SortKey: cursorString("5"), ID: 5}, []byte(testCursorSecret))
	if err != nil {
		t.Fatalf("EncodeCursor failed: %v", err)
	}
	if _, ok := VerifyAndDecode(token, []byte(testCursorSecret)); ok {
		t.Fatal("token codec must not decode a cursor payload")
	}
}

// TestResolveCursorLimit 验证 limit 的默认值与边界：默认 20、[1,100] 之外一律报错
// （不做截断），保证"客户端以为取了多少条"与"服务端实际取了多少条"一致。
func TestResolveCursorLimit(t *testing.T) {
	if got, err := ResolveCursorLimit(nil); err != nil || got != CursorDefaultLimit {
		t.Fatalf("nil limit: got (%d, %v), want (%d, nil)", got, err, CursorDefaultLimit)
	}

	for _, tc := range []struct {
		limit uint32
		ok    bool
	}{
		{1, true},
		{100, true},
		{0, false},
		{101, false},
	} {
		got, err := ResolveCursorLimit(&tc.limit)
		if tc.ok && err != nil {
			t.Fatalf("limit %d: unexpected error %v", tc.limit, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("limit %d: expected error, got %d", tc.limit, got)
		}
	}
}
