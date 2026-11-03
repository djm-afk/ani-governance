package service

import (
	"context"
	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	network "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	vm "github.com/zhangzhe-ctrl/ani-resource-service/api/vm/v1"
	view "go-wind-admin/api/gen/go/catalog/service/v1"
	vmview "go-wind-admin/api/gen/go/vm/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
)

type vmNetworkInterfaceClient interface {
	GetNetworkInterfaces(context.Context, string, string, string) (*network.NetworkInterfaces, error)
}

func (s *VMService) GetVMNetworkInterfaces(ctx context.Context, r *view.GetVMNetworkInterfacesRequest) (*view.NetworkInterfaces, error) {
	if r == nil || !canonicalCatalogUUID(r.VmId) {
		return nil, vmInvalid()
	}
	t, a, e := s.networkInterfaceScope(ctx, "network-interfaces")
	if e != nil {
		return nil, e
	}
	c, ok := s.binding.client.(vmNetworkInterfaceClient)
	if !ok {
		return nil, kratoserrors.ServiceUnavailable("VM_ADAPTER_UNAVAILABLE", "VM network interface owner unavailable")
	}
	reply, e := c.GetNetworkInterfaces(ctx, t, a, r.VmId)
	if e != nil {
		return nil, e
	}
	return wireNetworkInterfaces(reply)
}
func (s *VMService) SetVMNetworkInterface(ctx context.Context, r *view.SetVMNetworkInterfaceRequest) (*vmview.VMOperation, error) {
	if r == nil || !canonicalCatalogUUID(r.VmId) || !validNetworkInterfaceSelection(r.Slot, r.Data) {
		return nil, vmInvalid()
	}
	t, a, e := s.networkInterfaceScope(ctx, "set-network-interface")
	if e != nil {
		return nil, e
	}
	d := r.Data
	if d.Mode == "manual" {
		if e = authorizeNetworkInterface(ctx, s.binding.authorization, data.NetworkVNicReadPath, "GET"); e != nil {
			return nil, e
		}
	}
	intent := &vm.SetNetworkInterfaceRequest{TenantId: t, VmId: r.VmId, Slot: r.Slot, Mode: d.Mode, VnicId: d.VnicId, ExpectedVersion: d.ExpectedVersion, IdempotencyKey: d.IdempotencyKey}
	canonical, e := vm.CanonicalCommandIntent(intent)
	if e != nil {
		return nil, e
	}
	reply, e := s.binding.client.Lookup(ctx, t, a, &vm.LookupCommandReplayRequest{TenantId: t, VmId: r.VmId, Action: "set-network-interface", IdempotencyKey: d.IdempotencyKey, CanonicalIntent: canonical})
	if e != nil && !vmNotFound(e) {
		return nil, e
	}
	if vmNotFound(e) {
		if e = s.canConfigureNetwork(ctx, r.VmId); e != nil {
			return nil, e
		}
		reply, e = s.binding.client.Change(ctx, t, a, "set-network-interface", intent)
		if e != nil {
			return nil, e
		}
	}
	if reply == nil || reply.VmId != r.VmId || reply.Id == "" {
		return nil, kratoserrors.ServiceUnavailable("VM_INVALID_RESPONSE", "VM operation identity mismatch")
	}
	out := new(vmview.VMOperation)
	e = vmWire(reply, out)
	return out, e
}
