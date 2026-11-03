package service

import (
	"context"
	"encoding/json"
	"github.com/go-kratos/kratos/v2/errors"
	network "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	view "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"testing"
)

type vnicProbe struct {
	NetworkTenantClient
	NetworkVNicClient
	tenant, actor string
	wrongTenant   bool
	request       *network.ListVNicsRequest
	createRequest *network.CreateVNicRequest
}

func (p *vnicProbe) CreateVNic(ctx context.Context, t, a string, r *network.CreateVNicRequest) (*network.CreateVNicResponse, error) {
	p.createRequest = proto.Clone(r).(*network.CreateVNicRequest)
	reply, err := p.GetVNic(ctx, t, a, &network.GetVNicRequest{VnicId: "vnic_0123456789abcdef0123456789abcdef"})
	if err != nil {
		return nil, err
	}
	reply.Vnic.RequestedIp, reply.Vnic.RequestedMac = r.RequestedIp, r.RequestedMac
	reply.Vnic.State = network.ResourceState_RESOURCE_STATE_PROVISIONING
	reply.Vnic.PrimaryIp, reply.Vnic.MacAddress, reply.Vnic.Ips = "", "", nil
	return &network.CreateVNicResponse{Vnic: reply.Vnic}, nil
}

func (p *vnicProbe) GetVNic(_ context.Context, t, a string, r *network.GetVNicRequest) (*network.GetVNicResponse, error) {
	p.tenant, p.actor = t, a
	if p.wrongTenant {
		t = "22222222-2222-4222-8222-222222222222"
	}
	return &network.GetVNicResponse{Vnic: &network.VNic{Id: r.VnicId, TenantId: t, VpcId: "vpc_0123456789abcdef0123456789abcdef", SubnetId: "subnet_0123456789abcdef0123456789abcdef", State: network.ResourceState_RESOURCE_STATE_AVAILABLE, Version: 3, MacAddress: "02:00:00:00:00:01", PrimaryIp: "10.0.0.7", Ips: []string{"10.0.0.7"}, BindingState: "reserved", Bindings: []*network.VNicBinding{{BindingId: "bind-1", ConsumerKind: "vm", ConsumerId: "33333333-3333-4333-8333-333333333333", Slot: "primary", Target: true, RequestedRevision: 4}}}}, nil
}
func (p *vnicProbe) ListVNics(_ context.Context, t, a string, r *network.ListVNicsRequest) (*network.ListVNicsResponse, error) {
	p.tenant, p.actor = t, a
	p.request = proto.Clone(r).(*network.ListVNicsRequest)
	return &network.ListVNicsResponse{Total: 42}, nil
}
func TestVNicTrustedScopeKeepsReservationAndRejectsForeignResponse(t *testing.T) {
	const id = "vnic_0123456789abcdef0123456789abcdef"
	for _, subject := range []struct {
		typ   auth.SubjectType
		id    uint32
		actor string
	}{{auth.SubjectUser, 7, "governance:user:7"}, {auth.SubjectAPIKey, 42, "governance:access-key:42"}} {
		p := &vnicProbe{}
		s := NewNetworkService(p, &tenantProbe{})
		ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: subject.typ, ID: subject.id, TenantID: 5})
		got, e := s.GetVNic(ctx, &view.GetVNicRequest{VnicId: id})
		if e != nil || p.tenant != "11111111-1111-4111-8111-111111111111" || p.actor != subject.actor || got.Vnic.State != "available" || len(got.Vnic.Bindings) != 1 || !got.Vnic.Bindings[0].Target || got.Vnic.Bindings[0].Current {
			t.Fatal("trusted reservation view lost", got, e)
		}
		p.wrongTenant = true
		if _, e = s.GetVNic(ctx, &view.GetVNicRequest{VnicId: id}); errors.Code(e) != 503 {
			t.Fatal("foreign VNIC accepted", e)
		}
	}
}
func TestVNicListPreservesRandomPageAndTotal(t *testing.T) {
	p := &vnicProbe{}
	s := NewNetworkService(p, &tenantProbe{})
	ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7, TenantID: 5})
	got, e := s.ListVNics(ctx, &view.ListVNicsRequest{Page: proto.Uint32(9), PageSize: proto.Uint32(2), Name: "  lab  ", BindingState: "free"})
	if e != nil || got.Total != 42 || got.Items == nil || len(got.Items) != 0 || p.request.GetPage() != 9 || p.request.GetPageSize() != 2 || p.request.Name != "lab" {
		t.Fatal("random page or authoritative total lost", got, e)
	}
	if _, e = s.ListVNics(ctx, &view.ListVNicsRequest{BindableToKind: "vm"}); errors.Code(e) != 400 {
		t.Fatal("unpaired bindability admitted", e)
	}
}

func TestVNicCreatePreservesRequestedAddressIntent(t *testing.T) {
	for _, intent := range []struct{ ip, mac string }{{}, {"10.0.0.42", "02:00:00:00:00:42"}} {
		p := &vnicProbe{}
		s := NewNetworkService(p, &tenantProbe{})
		ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7, TenantID: 5})
		raw, err := json.Marshal(map[string]any{"data": map[string]string{"name": "lab", "subnet_id": "subnet_0123456789abcdef0123456789abcdef", "idempotency_key": "33333333-3333-4333-8333-333333333333", "requested_ip": intent.ip, "requested_mac": intent.mac}})
		if err != nil {
			t.Fatal(err)
		}
		var request view.CreateVNicRequest
		if err = protojson.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		out, err := s.CreateVNic(ctx, &request)
		if err != nil || p.createRequest == nil || p.createRequest.RequestedIp != intent.ip || p.createRequest.RequestedMac != intent.mac || p.createRequest.IdempotencyKey != request.Data.IdempotencyKey || out.Vnic.RequestedIp != intent.ip || out.Vnic.RequestedMac != intent.mac || out.Vnic.PrimaryIp != "" || out.Vnic.MacAddress != "" {
			t.Fatal("requested allocation intent was lost or presented as an observed address", out, p.createRequest, err)
		}
		raw, err = protojson.Marshal(out.Vnic)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err = json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if intent.ip != "" && (fields["requested_ip"] != intent.ip || fields["requested_mac"] != intent.mac) {
			t.Fatal("requested address JSON names changed", fields)
		}
	}
}
func TestNetworkSelectionDoesNotTreatTargetAsEffective(t *testing.T) {
	for _, state := range []string{"configuring", "ready", "failed"} {
		t.Run(state, func(t *testing.T) {
			pending := state == "ready"
			in := &network.NetworkInterfaces{Version: 9, Supported: true, Items: []*network.NetworkInterface{{Slot: "primary", Desired: &network.NetworkInterfaceChoice{Mode: "manual", VnicId: "vnic_0123456789abcdef0123456789abcdef", Ips: []string{"10.0.0.8"}}, Applied: &network.NetworkInterfaceChoice{Mode: "automatic"}, RequestedRevision: 9, AppliedRevision: 8, PendingRestart: pending, CurrentIps: []string{"10.0.0.7"}, ConfigurationState: state, OperationId: "33333333-3333-4333-8333-333333333333", OperationReason: "SECURITY_GROUP_SYNC_PENDING"}}}
			out, e := wireNetworkInterfaces(in)
			if e != nil || out.Items[0].PendingRestart != pending || out.Items[0].EffectiveNow || out.Items[0].CurrentIps[0] != "10.0.0.7" || out.Items[0].Desired.Ips[0] != "10.0.0.8" || out.Items[0].ConfigurationState != state || out.Items[0].OperationId != in.Items[0].OperationId || out.Items[0].OperationReason != in.Items[0].OperationReason {
				t.Fatal("configuration progress or current network facts lost", out, e)
			}
		})
	}
	for _, d := range []*view.SetNetworkInterfaceData{{Mode: "manual", ExpectedVersion: 9, IdempotencyKey: "33333333-3333-4333-8333-333333333333"}, {Mode: "automatic", VnicId: "vnic_0123456789abcdef0123456789abcdef", ExpectedVersion: 9, IdempotencyKey: "33333333-3333-4333-8333-333333333333"}} {
		if validNetworkInterfaceSelection("primary", d) {
			t.Fatal("invalid choice admitted", d)
		}
	}
}

type retainedVNicSGProbe struct {
	NetworkTenantClient
	NetworkSecurityGroupClient
}

func (*retainedVNicSGProbe) ListSecurityGroupBindings(_ context.Context, _, _ string, r *network.ListSecurityGroupBindingsRequest) (*network.ListSecurityGroupBindingsResponse, error) {
	return &network.ListSecurityGroupBindingsResponse{Total: 1, Items: []*network.SecurityGroupBinding{{TargetType: "vnic", TargetId: "vnic_0123456789abcdef0123456789abcdef", SecurityGroupIds: []string{r.SecurityGroupId}, AppliedSecurityGroupIds: []string{r.SecurityGroupId}, State: "applied", Version: 4}}}, nil
}
func TestRetainedVNicRemainsVisibleAsSecurityGroupReference(t *testing.T) {
	s := NewNetworkService(&retainedVNicSGProbe{}, &tenantProbe{})
	ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7, TenantID: 5})
	out, e := s.ListSecurityGroupBindings(ctx, &view.ListSecurityGroupBindingsRequest{SecurityGroupId: "sg_0123456789abcdef0123456789abcdef", TargetType: "vnic"})
	if e != nil || out.Total != 1 || len(out.Items) != 1 || out.Items[0].TargetType != "vnic" || out.Items[0].TargetId != "vnic_0123456789abcdef0123456789abcdef" {
		t.Fatal("retained manual VNIC reference hidden", out, e)
	}
}
