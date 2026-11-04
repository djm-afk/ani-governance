package service

import (
	"context"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/errors"
	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	catalogv1 "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func (p *vpcProbe) ListNetworkInstances(_ context.Context, tenant, actor, _, _ string, _, _ uint32) (*networkv1.ListNetworkInstancesResponse, error) {
	p.scope(tenant, actor)
	return &networkv1.ListNetworkInstancesResponse{}, p.err
}

type associatedInstanceProbe struct {
	*vpcProbe
	reply           *networkv1.ListNetworkInstancesResponse
	vpcID, subnetID string
	page, pageSize  uint32
}

func (p *associatedInstanceProbe) ListNetworkInstances(_ context.Context, tenant, actor, vpcID, subnetID string, page, pageSize uint32) (*networkv1.ListNetworkInstancesResponse, error) {
	p.scope(tenant, actor)
	p.vpcID, p.subnetID, p.page, p.pageSize = vpcID, subnetID, page, pageSize
	return p.reply, p.err
}

func TestNetworkAssociatedInstancesPreserveTenantAndUnknownConsumers(t *testing.T) {
	const tenant = "11111111-1111-4111-8111-111111111111"
	ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7, TenantID: 5})
	p := &associatedInstanceProbe{vpcProbe: &vpcProbe{}, reply: &networkv1.ListNetworkInstancesResponse{Total: 2, Items: []*networkv1.AssociatedInstance{
		{TenantId: tenant, ConsumerType: "virtual_machine", ConsumerId: "vm-id", ConsumerName: "vm", ConsumerState: "running", SubnetId: testSubnetID,
			SubnetName: "platform-subnet", Slot: "primary", AttachmentId: "attachment-a", AttachmentState: "reserved", AttachmentReason: "PENDING",
			ObservationStale: true, Addresses: []*networkv1.NetworkAddress{{NicName: "eth0", Address: "10.0.0.2"}}},
		{TenantId: tenant, ConsumerType: "unknown", ConsumerId: "lost-owner", SubnetId: testSubnetID, AttachmentId: "attachment-b", AttachmentState: "releasing"},
	}}}
	s := NewNetworkService(p, &tenantProbe{})
	request := &catalogv1.ListNetworkInstancesRequest{VpcId: testVPCID, SubnetId: testSubnetID}
	out, err := s.ListNetworkInstances(ctx, request)
	if err != nil || p.vpcID != testVPCID || p.subnetID != testSubnetID || p.page != 1 || p.pageSize != 20 || out.Total != 2 || len(out.Items) != 2 {
		t.Fatal("association scope/page/total lost", out, err)
	}
	if first := out.Items[0]; first.ConsumerState != "running" || first.AttachmentState != "reserved" || first.AttachmentReason != "PENDING" || !first.ObservationStale || first.Addresses[0].Address != "10.0.0.2" {
		t.Fatal("independent attachment state or current addresses lost", first)
	}
	if out.Items[1].ConsumerType != "unknown" || out.Items[1].AttachmentId != "attachment-b" {
		t.Fatal("unknown consumer occupancy was removed", out)
	}
	raw, err := protojson.Marshal(out)
	if err != nil || strings.Contains(string(raw), "tenant") || !strings.Contains(string(raw), `"consumer_type"`) {
		t.Fatal("association public DTO changed", string(raw), err)
	}
	p.reply.Items[1].TenantId = "foreign"
	if _, err := s.ListNetworkInstances(ctx, request); errors.Code(err) != 503 {
		t.Fatal("cross-tenant association returned", err)
	}
	p.reply = &networkv1.ListNetworkInstancesResponse{}
	out, err = s.ListNetworkInstances(ctx, request)
	if err != nil || out.Items == nil || len(out.Items) != 0 || out.Total != 0 {
		t.Fatal("known network empty page", out, err)
	}
	for _, bad := range []*catalogv1.ListNetworkInstancesRequest{nil, {}, {VpcId: "bad"}, {SubnetId: testSubnetID, Page: proto.Uint32(0)}, {VpcId: testVPCID, PageSize: proto.Uint32(101)}} {
		calls := p.calls
		if _, err := s.ListNetworkInstances(ctx, bad); errors.Code(err) != 400 || p.calls != calls {
			t.Fatal("invalid association request reached Resource", bad, err)
		}
	}
}
