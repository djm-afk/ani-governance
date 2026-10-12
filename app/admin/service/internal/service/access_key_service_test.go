package service

import (
	accesskeyV1 "go-wind-admin/api/gen/go/access_key/service/v1"
	"go-wind-admin/pkg/localdeps/go-utils/trans"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"testing"
)

// TestCreateAccessKeyDataLegacyWireCompat 验证 §3.5：旧 Proto 客户端把
// CreateAccessKeyRequest.data 当作 AccessKey（name=2、expires_at=5、role_id=7）
// 发来的报文，改字段编号后用新服务解码仍应落在正确字段上。
//
// 复现方式：用当前读模型 AccessKey（保留旧编号）编出旧 wire 字节，再按旧外层
// 结构（data=1）包一层，然后交给新 CreateAccessKeyRequest 解码并逐字段核对。
func TestCreateAccessKeyDataLegacyWireCompat(t *testing.T) {
	legacyData, err := proto.Marshal(&accesskeyV1.AccessKey{
		Id:        trans.Ptr(uint32(9)),
		Name:      trans.Ptr("legacy-reader"),
		RoleId:    trans.Ptr(uint32(12)),
		ExpiresAt: timestamppb.New(timestamppb.Now().AsTime()),
	})
	if err != nil {
		t.Fatalf("marshal legacy data failed: %v", err)
	}

	// 旧外层：CreateAccessKeyRequest { AccessKey data = 1; }
	var wire []byte
	wire = protowire.AppendTag(wire, 1, protowire.BytesType)
	wire = protowire.AppendBytes(wire, legacyData)

	var req accesskeyV1.CreateAccessKeyRequest
	if err := proto.Unmarshal(wire, &req); err != nil {
		t.Fatalf("旧报文应能被新服务解码，实际 %v", err)
	}

	require := func(cond bool, msg string) {
		if !cond {
			t.Fatalf("%s", msg)
		}
	}
	require(req.GetData().GetName() == "legacy-reader", "旧报文的 name（字段 2）应解码到 CreateAccessKeyData.name")
	require(req.GetData().GetRoleId() == 12, "旧报文的 role_id（字段 7）应解码到 CreateAccessKeyData.role_id")
	require(req.GetData().GetExpiresAt() != nil, "旧报文的 expires_at（字段 5）应解码到 CreateAccessKeyData.expires_at")

	// 旧报文中被占用过的编号（1/3/4/6/200）已 reserved：即便客户端误发也不得被吸入新字段
	require(req.GetData().GetIdempotencyKey() == "", "idempotency_key（新编号 8）不得被旧报文的其它字段污染")
}

func TestAccessKeyFieldMaskJSON(t *testing.T) {
	var req accesskeyV1.UpdateAccessKeyRequest
	if err := protojson.Unmarshal([]byte(`{"key_id":1,"data":{"is_active":false,"expires_at":null,"role_id":2},"update_mask":"isActive,expiresAt,roleId"}`), &req); err != nil {
		t.Fatal(err)
	}
	if err := validateAccessKeyUpdate(&req); err != nil {
		t.Fatal(err)
	}
	if got := req.UpdateMask.Paths[0]; got != "is_active" {
		t.Fatalf("decoded %s", got)
	}
	if err := protojson.Unmarshal([]byte(`{"key_id":1,"data":{"is_active":false},"update_mask":"is_active"}`), &req); err == nil {
		t.Fatal("accepted snake_case mask value")
	}
}
func TestAccessKeyUpdateValidation(t *testing.T) {
	for _, path := range []string{"access_key", "secret_key", "tenant_id", "created_by", "id", "unknown", ""} {
		req := &accesskeyV1.UpdateAccessKeyRequest{KeyId: 1, Data: &accesskeyV1.AccessKey{}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}}}
		if validateAccessKeyUpdate(req) == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if validateAccessKeyUpdate(&accesskeyV1.UpdateAccessKeyRequest{KeyId: 1, Data: &accesskeyV1.AccessKey{}}) == nil {
		t.Fatal("accepted empty mask")
	}
	req := &accesskeyV1.UpdateAccessKeyRequest{KeyId: 1, Data: &accesskeyV1.AccessKey{IsActive: trans.Ptr(false)}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"is_active", "expires_at"}}}
	if err := validateAccessKeyUpdate(req); err != nil {
		t.Fatal(err)
	}
	req.UpdateMask.Paths = []string{"role_id"}
	if validateAccessKeyUpdate(req) == nil {
		t.Fatal("accepted zero role")
	}
}
