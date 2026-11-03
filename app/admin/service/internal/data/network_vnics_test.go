package data

import (
	"context"
	network "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"testing"
	"time"
)

type vnicRPCProbe struct {
	network.NetworkServiceClient
	t     *testing.T
	calls int
	actor string
}

func (p *vnicRPCProbe) CreateVNic(ctx context.Context, in *network.CreateVNicRequest, _ ...grpc.CallOption) (*network.CreateVNicResponse, error) {
	p.calls++
	md, _ := metadata.FromOutgoingContext(ctx)
	if in.TargetTenantId != "11111111-1111-4111-8111-111111111111" || len(md.Get("x-ani-tenant-id")) != 1 || md.Get("x-ani-tenant-id")[0] != in.TargetTenantId || len(md.Get("x-ani-actor")) != 1 || md.Get("x-ani-actor")[0] != p.actor || len(md.Get("authorization")) != 0 || len(md.Get("evil")) != 0 {
		p.t.Fatal("untrusted VNIC metadata", md, in)
	}
	if _, ok := ctx.Deadline(); !ok {
		p.t.Fatal("unbounded call")
	}
	if in.RequestedIp != "10.0.0.42" || in.RequestedMac != "02:00:00:00:00:42" {
		p.t.Fatal("requested allocation intent was not forwarded", in)
	}
	return &network.CreateVNicResponse{}, nil
}
func TestVNicClientUsesTrustedTenantAndRetainsOriginalKey(t *testing.T) {
	for _, a := range []string{"governance:user:7", "governance:access-key:42"} {
		p := &vnicRPCProbe{t: t, actor: a}
		c := &NetworkClient{client: p, timeout: time.Second}
		r := &network.CreateVNicRequest{TargetTenantId: "forged", Name: "lab", IdempotencyKey: "33333333-3333-4333-8333-333333333333", RequestedIp: "10.0.0.42", RequestedMac: "02:00:00:00:00:42"}
		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "public", "evil", "header", "x-ani-tenant-id", "forged"))
		if _, e := c.CreateVNic(ctx, "11111111-1111-4111-8111-111111111111", a, r); e != nil {
			t.Fatal(e)
		}
		if p.calls != 1 || r.TargetTenantId != "forged" || r.IdempotencyKey != "33333333-3333-4333-8333-333333333333" || r.RequestedIp != "10.0.0.42" || r.RequestedMac != "02:00:00:00:00:42" {
			t.Fatal("request mutated", r)
		}
	}
}
