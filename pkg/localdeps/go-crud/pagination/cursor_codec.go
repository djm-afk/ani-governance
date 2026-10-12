package pagination

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// signedCursorPrefix 是签名游标的版本前缀，与旧 token 的 "v2." 前缀区分，
// 使两种游标在同一进程内不可能互相解析。
const signedCursorPrefix = "c1."

// cursorPayloadVersion 是游标载荷的版本号；版本不匹配一律拒绝（fail-closed），
// 便于未来载荷结构演进时明确失效而不是误读旧格式。
const cursorPayloadVersion = 1

// maxCursorPayloadLen 是 base64 载荷的长度上限，用于拒绝客户端发送的超大 blob
// （避免对兆级 base64 做解码 + JSON 解析耗 CPU）。
const maxCursorPayloadLen = 2048

// 游标分页的每页条数约束。与 page/offset 分页不同，游标分页不截断非法值，
// 而是直接拒绝（见 ResolveCursorLimit），避免"以为取了 100 条实际只取 20 条"这类静默偏差。
const (
	// CursorDefaultLimit 是未传 limit 时的默认每页条数。
	CursorDefaultLimit = 20
	// CursorMaxLimit 是允许的最大每页条数。
	CursorMaxLimit = 100
)

// ErrInvalidCursor 表示游标无法通过校验：格式非法、签名不符、或与本次请求的
// 资源、租户上下文、筛选与排序绑定不一致。调用方应把它映射为 400。
var ErrInvalidCursor = errors.New("invalid or tampered pagination cursor")

// CursorBinding 描述一个游标所绑定的请求上下文。解码时会逐项比对，
// 任何一项不一致都视为无效游标，从而保证游标不可跨资源、跨租户、
// 跨筛选条件复用。
type CursorBinding struct {
	// Resource 资源标识，如 "identity.user"。
	Resource string
	// Context 上下文类型："tenant"、"platform" 或 "system"。
	// 平台/系统上下文下租户隔离是放行的（列表本身跨租户），必须与租户上下文区分，
	// 否则同一条游标会在两种上下文之间被误用。
	Context string
	// TenantID 可信租户 ID（取自服务端 viewer，不信任入站参数）；平台/系统上下文为 0。
	TenantID uint32
	// FilterKey 规范化筛选与排序的指纹（十六进制 sha256）。
	FilterKey string
}

// Cursor 是一条游标的解码结果：上一页最后一条记录的排序值与唯一 ID。
type Cursor struct {
	// SortKey 排序列取值的规范字符串形式，按列的取值类型解析回驱动值。
	// 为 nil 表示上一页最后一条记录的排序值本身是 SQL NULL——必须与"空字符串"
	// 区分开，否则键集条件无法正确穿过 NULL 区段（见 entgo/cursor.go 的键集构造）。
	SortKey *string
	// ID 上一页最后一条记录的唯一 ID（排序兜底列）。
	ID uint64
}

// cursorPayload 是游标在序列化时的载荷结构。字段名保持单字符以压缩长度，
// 客户端不解析该结构。
type cursorPayload struct {
	V int     `json:"v"`
	R string  `json:"r"`
	C string  `json:"c"`
	T uint32  `json:"t"`
	D string  `json:"d"`
	K *string `json:"k"`
	I uint64  `json:"i"`
}

// EncodeCursor 把绑定信息与游标内容序列化为带 HMAC-SHA256 签名的游标。
// 格式：c1.<base64url 无填充(json 载荷)>.<hex(hmac)>
// 密钥为空时拒绝生成：游标分页不接受无签名游标，避免伪造。
func EncodeCursor(binding CursorBinding, cursor Cursor, secret []byte) (string, error) {
	if len(secret) == 0 {
		return "", errors.New("pagination cursor secret is not configured")
	}

	payload, err := json.Marshal(cursorPayload{
		V: cursorPayloadVersion,
		R: binding.Resource,
		C: binding.Context,
		T: binding.TenantID,
		D: binding.FilterKey,
		K: cursor.SortKey,
		I: cursor.ID,
	})
	if err != nil {
		return "", fmt.Errorf("marshal pagination cursor failed: %w", err)
	}

	encoded := base64.RawURLEncoding.EncodeToString(payload)
	// 编解码长度范围必须一致：否则会生成一个必然被 DecodeCursor 拒绝的游标
	// （排序列如长字符串列没有存储长度上限时，边界值就会触发），
	// 使客户端拿到的 next_cursor 原样回传却 400。
	if len(encoded) > maxCursorPayloadLen {
		return "", fmt.Errorf("%w: payload too large (%d > %d)", ErrInvalidCursor, len(encoded), maxCursorPayloadLen)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(encoded))
	return signedCursorPrefix + encoded + "." + hex.EncodeToString(mac.Sum(nil)), nil
}

// DecodeCursor 校验并解码分页游标。
//
// 任一步失败（前缀不符、载荷超长、签名不符、JSON 非法、版本不符、
// 与本次请求的绑定不一致）都返回 ErrInvalidCursor，绝不降级为"当作首页处理"，
// 否则被篡改的游标会静默变成另一页数据。
func DecodeCursor(token string, binding CursorBinding, secret []byte) (*Cursor, error) {
	if token == "" {
		return nil, ErrInvalidCursor
	}
	if !strings.HasPrefix(token, signedCursorPrefix) {
		return nil, ErrInvalidCursor
	}
	if len(secret) == 0 {
		return nil, ErrInvalidCursor
	}

	body := strings.TrimPrefix(token, signedCursorPrefix)
	dot := strings.LastIndex(body, ".")
	if dot < 0 {
		return nil, ErrInvalidCursor
	}
	encoded, sigHex := body[:dot], body[dot+1:]
	if len(encoded) == 0 || len(encoded) > maxCursorPayloadLen {
		return nil, ErrInvalidCursor
	}

	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(encoded))
	if !hmac.Equal([]byte(sigHex), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return nil, ErrInvalidCursor
	}

	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrInvalidCursor
	}
	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, ErrInvalidCursor
	}
	if payload.V != cursorPayloadVersion {
		return nil, ErrInvalidCursor
	}

	if payload.R != binding.Resource ||
		payload.C != binding.Context ||
		payload.T != binding.TenantID ||
		payload.D != binding.FilterKey {
		return nil, ErrInvalidCursor
	}

	return &Cursor{SortKey: payload.K, ID: payload.I}, nil
}

// ResolveCursorLimit 校验并归一化游标分页的每页条数：
// 未传（nil）取 CursorDefaultLimit；超出 [1, CursorMaxLimit] 返回错误。
// 这里刻意不做 clampLimit 式的截断：合同要求非法值明确拒绝。
func ResolveCursorLimit(limit *uint32) (int, error) {
	if limit == nil {
		return CursorDefaultLimit, nil
	}
	value := int(*limit)
	if value < 1 || value > CursorMaxLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d, got %d", CursorMaxLimit, value)
	}
	return value, nil
}
