package service

import (
	"context"
	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	network "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	view "go-wind-admin/api/gen/go/catalog/service/v1"
	instanceview "go-wind-admin/api/gen/go/instance/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type instanceNetworkInterfaceClient interface {
	GetNetworkInterfaces(context.Context, string, string, string) (*network.NetworkInterfaces, error)
	SetNetworkInterface(context.Context, string, string, *res.SetNetworkInterfaceRequest) (*res.OperationResponse, error)
}

func validNetworkInterfaceSelection(slot string, d *view.SetNetworkInterfaceData) bool {
	return slot == "primary" && d != nil && d.ExpectedVersion > 0 && canonicalCatalogUUID(d.IdempotencyKey) && ((d.Mode == "automatic" && d.VnicId == "") || (d.Mode == "manual" && vnicIDPattern.MatchString(d.VnicId)))
}
func wireNetworkInterfaces(in *network.NetworkInterfaces) (*view.NetworkInterfaces, error) {
	if in == nil || in.Version < 1 {
		return nil, invalidNetworkReply()
	}
	for _, item := range in.Items {
		if item == nil || item.Slot != "primary" || item.RequestedRevision < 0 || item.AppliedRevision < 0 {
			return nil, invalidNetworkReply()
		}
	}
	out := new(view.NetworkInterfaces)
	if e := securityGroupWire(in, out); e != nil {
		return nil, e
	}
	if out.Items == nil {
		out.Items = []*view.NetworkInterface{}
	}
	return out, nil
}
func (s *InstanceService) GetInstanceNetworkInterfaces(ctx context.Context, r *view.GetInstanceNetworkInterfacesRequest) (*view.NetworkInterfaces, error) {
	if r == nil || !instanceUUID(r.InstanceId) {
		return nil, kratoserrors.BadRequest("INVALID_INSTANCE_REQUEST", "invalid instance ID")
	}
	t, a, e := s.networkInterfaceScope(ctx, data.InstanceNetworkInterfacesPath, "GET")
	if e != nil {
		return nil, e
	}
	c, ok := s.binding.client.(instanceNetworkInterfaceClient)
	if !ok {
		return nil, kratoserrors.ServiceUnavailable("INSTANCE_ADAPTER_UNAVAILABLE", "instance network interface owner unavailable")
	}
	reply, e := c.GetNetworkInterfaces(ctx, t, a, r.InstanceId)
	if e != nil {
		return nil, e
	}
	return wireNetworkInterfaces(reply)
}
func (s *InstanceService) SetInstanceNetworkInterface(ctx context.Context, r *view.SetInstanceNetworkInterfaceRequest) (*instanceview.Acceptance, error) {
	if r == nil || !instanceUUID(r.InstanceId) || !validNetworkInterfaceSelection(r.Slot, r.Data) {
		return nil, kratoserrors.BadRequest("INVALID_INSTANCE_REQUEST", "invalid network interface command")
	}
	t, a, e := s.networkInterfaceScope(ctx, data.InstanceNetworkInterfacePath, "PUT")
	if e != nil {
		return nil, e
	}
	d := r.Data
	if d.Mode == "manual" {
		if e = authorizeNetworkInterface(ctx, s.binding.authorization, data.NetworkVNicReadPath, "GET"); e != nil {
			return nil, e
		}
	}
	c, ok := s.binding.client.(instanceNetworkInterfaceClient)
	if !ok {
		return nil, kratoserrors.ServiceUnavailable("INSTANCE_ADAPTER_UNAVAILABLE", "instance network interface owner unavailable")
	}
	canonical, e := res.CanonicalNetworkInterfaceIntent(r.InstanceId, r.Slot, d.Mode, d.VnicId, d.ExpectedVersion)
	if e != nil {
		return nil, kratoserrors.BadRequest("INVALID_INSTANCE_REQUEST", "invalid network interface intent")
	}
	reply, e := s.binding.client.Lookup(ctx, t, a, &res.LookupCommandReplayRequest{TenantId: t, InstanceId: r.InstanceId, Action: "set-network-interface", IdempotencyKey: d.IdempotencyKey, CanonicalIntent: canonical})
	if e == nil {
		return instanceCommandAcceptance(reply, r.InstanceId, true)
	}
	if status.Code(e) != codes.NotFound {
		return nil, e
	}
	if e = s.canConfigureNetwork(ctx, r.InstanceId); e != nil {
		return nil, e
	}
	if !s.binding.newIntentsEnabled {
		return nil, kratoserrors.ServiceUnavailable("INSTANCE_ACTION_UNAVAILABLE", "instance action unavailable")
	}
	reply, e = c.SetNetworkInterface(ctx, t, a, &res.SetNetworkInterfaceRequest{TenantId: t, InstanceId: r.InstanceId, Slot: r.Slot, Mode: d.Mode, VnicId: d.VnicId, ExpectedVersion: d.ExpectedVersion, IdempotencyKey: d.IdempotencyKey})
	if e != nil {
		return nil, e
	}
	return instanceCommandAcceptance(reply, r.InstanceId, false)
}
