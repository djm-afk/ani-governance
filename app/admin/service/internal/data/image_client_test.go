package data

import (
	"context"
	imagev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/image/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"testing"
	"time"
)

type imageClientProbe struct {
	imagev1.TenantImageServiceClient
	t     *testing.T
	actor string
	calls *int
}

func (p imageClientProbe) GetImageSpace(ctx context.Context, r *imagev1.GetImageSpaceRequest, _ ...grpc.CallOption) (*imagev1.GetImageSpaceResponse, error) {
	(*p.calls)++
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok || len(md) != 3 || len(md.Get("x-ani-tenant-id")) != 1 || len(md.Get("x-ani-actor")) != 1 || md.Get("x-ani-actor")[0] != p.actor || md.Get("x-ani-tenant-id")[0] != r.TenantId {
		p.t.Fatal("untrusted or duplicated outgoing metadata")
	}
	if _, ok := ctx.Deadline(); !ok {
		p.t.Fatal("missing bounded deadline")
	}
	return &imagev1.GetImageSpaceResponse{Space: &imagev1.ImageSpace{TenantId: r.TenantId}}, nil
}
func TestImageClientRebuildsIdentity(t *testing.T) {
	tenant := "11111111-1111-4111-8111-111111111111"
	for _, actor := range []string{"governance:user:7", "governance:access-key:42"} {
		calls := 0
		c := &ImageClient{client: imageClientProbe{t: t, actor: actor, calls: &calls}, timeout: time.Second}
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-ani-tenant-id", "foreign", "x-ani-actor", "forged", "authorization", "secret", "x-ani-operator", "attacker"))
		r := &imagev1.GetImageSpaceRequest{TenantId: "body-tenant"}
		if _, err := c.GetImageSpace(ctx, tenant, actor, r); err != nil || calls != 1 {
			t.Fatal(err)
		}
		if r.TenantId != "body-tenant" {
			t.Fatal("client mutated caller request")
		}
		for _, bad := range []string{"governance:user:0", "governance:user:07", "governance:user:4294967296", "governance:machine:7"} {
			if _, err := c.GetImageSpace(ctx, tenant, bad, r); status.Code(err) != codes.Unauthenticated {
				t.Fatal("invalid actor accepted")
			}
		}
		if calls != 1 {
			t.Fatal("invalid identity reached transport")
		}
	}
	if _, _, err := NewImageClient(ImageClientConfig{}); err == nil {
		t.Fatal("insecure client accepted")
	}
}

type failedImageRPC struct {
	imagev1.TenantImageServiceClient
	err error
}

func (p failedImageRPC) GetImageSpace(context.Context, *imagev1.GetImageSpaceRequest, ...grpc.CallOption) (*imagev1.GetImageSpaceResponse, error) {
	return nil, p.err
}

func TestImageClientPreservesRPCStatus(t *testing.T) {
	for _, code := range []codes.Code{codes.DeadlineExceeded, codes.Unavailable, codes.FailedPrecondition, codes.PermissionDenied} {
		downstream, err := status.New(code, "private downstream failure").WithDetails(&errdetails.ErrorInfo{Reason: "SPACE_NOT_READY", Domain: "resource.image"})
		if err != nil {
			t.Fatal(err)
		}
		c := &ImageClient{client: failedImageRPC{err: downstream.Err()}, timeout: time.Second}
		_, got := c.GetImageSpace(context.Background(), "11111111-1111-4111-8111-111111111111", "governance:user:7", &imagev1.GetImageSpaceRequest{})
		if !proto.Equal(status.Convert(got).Proto(), downstream.Proto()) {
			t.Fatalf("downstream status or domain details changed: %v", got)
		}
	}
}
