package service

import (
	"context"
	network "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	view "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/pkg/middleware/auth"
	"testing"
)

type networkScopeAuthorizer struct {
	typ   auth.SubjectType
	id    uint32
	calls int
}

func (p *networkScopeAuthorizer) Authorize(context.Context, uint32, uint32, string, string) error {
	return data.ErrActionAuthorizationDenied
}
func (p *networkScopeAuthorizer) AuthorizeNetworkPrincipal(_ context.Context, principal *auth.Principal, _, _ string) error {
	p.typ, p.id = principal.Type, principal.ID
	p.calls++
	return nil
}

type vmNetworkReadProbe struct {
	VMOwnerClient
	tenant, actor string
}

func (p *vmNetworkReadProbe) GetNetworkInterfaces(_ context.Context, tenant, actor, id string) (*network.NetworkInterfaces, error) {
	p.tenant, p.actor = tenant, actor
	return &network.NetworkInterfaces{Version: 2, Supported: true, Items: []*network.NetworkInterface{{Slot: "primary", Desired: &network.NetworkInterfaceChoice{Mode: "automatic"}}}}, nil
}

type instanceNetworkReadProbe struct {
	InstanceOwnerClient
	tenant, actor string
}

func (p *instanceNetworkReadProbe) GetNetworkInterfaces(_ context.Context, tenant, actor, id string) (*network.NetworkInterfaces, error) {
	p.tenant, p.actor = tenant, actor
	return &network.NetworkInterfaces{Version: 2, Supported: true, Items: []*network.NetworkInterface{{Slot: "primary", Desired: &network.NetworkInterfaceChoice{Mode: "automatic"}}}}, nil
}
func TestVMNetworkReadKeepsAPIKeyIdentityDistinctFromUser(t *testing.T) {
	current := &networkScopeAuthorizer{}
	owner := &vmNetworkReadProbe{}
	s := NewVMService(nil, NewVMBinding(owner, current, "cluster"), &tenantProbe{}, new(data.QuotaLedgerRepo), nil)
	ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectAPIKey, ID: 42, TenantID: 5})
	out, e := s.GetVMNetworkInterfaces(ctx, &view.GetVMNetworkInterfacesRequest{VmId: "33333333-3333-4333-8333-333333333333"})
	if e != nil || out == nil || current.typ != auth.SubjectAPIKey || current.id != 42 || current.calls != 1 || owner.actor != "governance:access-key:42" || owner.tenant != "11111111-1111-4111-8111-111111111111" {
		t.Fatal("API key treated as a user", out, e, current, owner)
	}
}
