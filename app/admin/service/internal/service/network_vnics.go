package service

import (
	"context"
	"github.com/go-kratos/kratos/v2/errors"
	network "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	view "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"google.golang.org/protobuf/proto"
	"regexp"
	"strings"
)

type NetworkVNicClient interface {
	CreateVNic(context.Context, string, string, *network.CreateVNicRequest) (*network.CreateVNicResponse, error)
	GetVNic(context.Context, string, string, *network.GetVNicRequest) (*network.GetVNicResponse, error)
	ListVNics(context.Context, string, string, *network.ListVNicsRequest) (*network.ListVNicsResponse, error)
	DeleteVNic(context.Context, string, string, *network.DeleteVNicRequest) (*network.DeleteVNicResponse, error)
	ListVNicBindingTargets(context.Context, string, string, *network.ListVNicBindingTargetsRequest) (*network.ListVNicBindingTargetsResponse, error)
}

var vnicIDPattern = regexp.MustCompile(`^vnic_[0-9a-f]{32}$`)

func (s *NetworkService) vnicScope(ctx context.Context) (NetworkVNicClient, string, string, error) {
	p, t, e := trustedOperator(ctx, s)
	if e != nil {
		return nil, "", "", e
	}
	a, e := p.Actor()
	if e != nil {
		return nil, "", "", e
	}
	c, ok := s.client.(NetworkVNicClient)
	if !ok {
		return nil, "", "", errors.ServiceUnavailable("NETWORK_UNAVAILABLE", "virtual NIC access unavailable")
	}
	return c, t, a, nil
}
func wireVNic(v *network.VNic, t, id string) (*view.VNic, error) {
	if v == nil || v.TenantId != t || !vnicIDPattern.MatchString(v.Id) || (id != "" && id != v.Id) || !vpcIDPattern.MatchString(v.VpcId) || !subnetIDPattern.MatchString(v.SubnetId) || v.Version < 1 || v.State < network.ResourceState_RESOURCE_STATE_PROVISIONING || v.State > network.ResourceState_RESOURCE_STATE_DELETED {
		return nil, invalidNetworkReply()
	}
	out := &view.VNic{Id: v.Id, Name: v.Name, Description: v.Description, VpcId: v.VpcId, SubnetId: v.SubnetId, State: strings.ToLower(strings.TrimPrefix(v.State.String(), "RESOURCE_STATE_")), Version: v.Version, MacAddress: v.MacAddress, PrimaryIp: v.PrimaryIp, Ips: append([]string{}, v.Ips...), RequestedIp: v.RequestedIp, RequestedMac: v.RequestedMac, SecurityGroupIds: append([]string{}, v.SecurityGroupIds...), ObservedAt: v.ObservedAt, ObservationStale: v.ObservationStale, Reason: v.Reason, LastOperationId: v.LastOperationId, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, BindingState: v.BindingState, VpcName: v.VpcName, SubnetName: v.SubnetName, SecurityGroupNames: append([]string{}, v.SecurityGroupNames...), Bindings: []*view.VNicBinding{}}
	for _, b := range v.Bindings {
		if b == nil || !canonicalCatalogUUID(b.ConsumerId) || b.Slot != "primary" || (b.ConsumerKind != "vm" && b.ConsumerKind != "container") {
			return nil, invalidNetworkReply()
		}
		out.Bindings = append(out.Bindings, &view.VNicBinding{BindingId: b.BindingId, ConsumerKind: b.ConsumerKind, ConsumerId: b.ConsumerId, Slot: b.Slot, RequestedRevision: b.RequestedRevision, AppliedRevision: b.AppliedRevision, Current: b.Current, Target: b.Target, ConsumerName: b.ConsumerName, ComputeType: b.ComputeType})
	}
	return out, nil
}
func (s *NetworkService) CreateVNic(ctx context.Context, r *view.CreateVNicRequest) (*view.VNicResponse, error) {
	if r == nil || r.Data == nil || !subnetIDPattern.MatchString(r.Data.SubnetId) || !canonicalCatalogUUID(r.Data.IdempotencyKey) {
		return nil, invalidSecurityGroupRequest()
	}
	c, t, a, e := s.vnicScope(ctx)
	if e != nil {
		return nil, e
	}
	d := r.Data
	out, e := c.CreateVNic(ctx, t, a, &network.CreateVNicRequest{Name: d.Name, Description: d.Description, SubnetId: d.SubnetId, IdempotencyKey: d.IdempotencyKey, RequestedIp: d.RequestedIp, RequestedMac: d.RequestedMac})
	if e != nil {
		return nil, mapNetworkError(e)
	}
	if out == nil {
		return nil, invalidNetworkReply()
	}
	v, e := wireVNic(out.Vnic, t, "")
	return &view.VNicResponse{Vnic: v}, e
}
func (s *NetworkService) GetVNic(ctx context.Context, r *view.GetVNicRequest) (*view.VNicResponse, error) {
	if r == nil || !vnicIDPattern.MatchString(r.VnicId) {
		return nil, invalidSecurityGroupRequest()
	}
	c, t, a, e := s.vnicScope(ctx)
	if e != nil {
		return nil, e
	}
	out, e := c.GetVNic(ctx, t, a, &network.GetVNicRequest{VnicId: r.VnicId})
	if e != nil {
		return nil, mapNetworkError(e)
	}
	if out == nil {
		return nil, invalidNetworkReply()
	}
	v, e := wireVNic(out.Vnic, t, r.VnicId)
	return &view.VNicResponse{Vnic: v}, e
}
func (s *NetworkService) DeleteVNic(ctx context.Context, r *view.DeleteVNicRequest) (*view.VNicResponse, error) {
	if r == nil || !vnicIDPattern.MatchString(r.VnicId) {
		return nil, invalidSecurityGroupRequest()
	}
	c, t, a, e := s.vnicScope(ctx)
	if e != nil {
		return nil, e
	}
	out, e := c.DeleteVNic(ctx, t, a, &network.DeleteVNicRequest{VnicId: r.VnicId})
	if e != nil {
		return nil, mapNetworkError(e)
	}
	if out == nil {
		return nil, invalidNetworkReply()
	}
	v, e := wireVNic(out.Vnic, t, r.VnicId)
	return &view.VNicResponse{Vnic: v}, e
}
func (s *NetworkService) ListVNics(ctx context.Context, r *view.ListVNicsRequest) (*view.ListVNicsResponse, error) {
	if r == nil {
		return nil, invalidSecurityGroupRequest()
	}
	p, z, e := resolveVPCPage(r.Page, r.PageSize)
	if e != nil {
		return nil, e
	}
	if (r.SubnetId != "" && !subnetIDPattern.MatchString(r.SubnetId)) || (r.BindableToKind == "") != (r.BindableToId == "") || (r.BindableToKind != "" && r.BindableToKind != "vm" && r.BindableToKind != "container") || (r.BindableToId != "" && !canonicalCatalogUUID(r.BindableToId)) {
		return nil, invalidSecurityGroupRequest()
	}
	if r.BindableToKind != "" {
		if e = s.authorizeVNicConsumer(ctx, r.BindableToKind); e != nil {
			return nil, e
		}
	}
	c, t, a, e := s.vnicScope(ctx)
	if e != nil {
		return nil, e
	}
	reply, e := c.ListVNics(ctx, t, a, &network.ListVNicsRequest{Page: proto.Uint32(p), PageSize: proto.Uint32(z), Name: strings.TrimSpace(r.Name), SubnetId: r.SubnetId, State: r.State, BindingState: r.BindingState, BindableToKind: r.BindableToKind, BindableToId: r.BindableToId})
	if e != nil {
		return nil, mapNetworkError(e)
	}
	if reply == nil || reply.Total < 0 || len(reply.Items) > int(z) {
		return nil, invalidNetworkReply()
	}
	out := &view.ListVNicsResponse{Items: []*view.VNic{}, Total: reply.Total}
	for _, v := range reply.Items {
		item, e := wireVNic(v, t, "")
		if e != nil {
			return nil, e
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
func (s *NetworkService) ListVNicBindingTargets(ctx context.Context, r *view.ListVNicBindingTargetsRequest) (*view.ListVNicBindingTargetsResponse, error) {
	if r == nil || !vnicIDPattern.MatchString(r.VnicId) || (r.ConsumerKind != "vm" && r.ConsumerKind != "container") {
		return nil, invalidSecurityGroupRequest()
	}
	p, z, e := resolveVPCPage(r.Page, r.PageSize)
	if e != nil {
		return nil, e
	}
	if e = s.authorizeVNicConsumer(ctx, r.ConsumerKind); e != nil {
		return nil, e
	}
	c, t, a, e := s.vnicScope(ctx)
	if e != nil {
		return nil, e
	}
	reply, e := c.ListVNicBindingTargets(ctx, t, a, &network.ListVNicBindingTargetsRequest{VnicId: r.VnicId, ConsumerKind: r.ConsumerKind, Name: strings.TrimSpace(r.Name), Page: proto.Uint32(p), PageSize: proto.Uint32(z)})
	if e != nil {
		return nil, mapNetworkError(e)
	}
	if reply == nil || reply.Total < 0 || len(reply.Items) > int(z) {
		return nil, invalidNetworkReply()
	}
	out := &view.ListVNicBindingTargetsResponse{Items: []*view.VNicBindingTarget{}, Total: reply.Total}
	for _, v := range reply.Items {
		if v == nil || v.ConsumerKind != r.ConsumerKind || !canonicalCatalogUUID(v.ConsumerId) || v.Version < 1 {
			return nil, invalidNetworkReply()
		}
		out.Items = append(out.Items, &view.VNicBindingTarget{ConsumerKind: v.ConsumerKind, ConsumerId: v.ConsumerId, Name: v.Name, Version: v.Version, State: v.State, SecurityGroupIds: append([]string{}, v.SecurityGroupIds...), ComputeType: v.ComputeType})
	}
	return out, nil
}
func (s *NetworkService) authorizeVNicConsumer(ctx context.Context, kind string) error {
	path := data.InstanceGetPath
	if kind == "vm" {
		path = data.VMGetPath
	}
	return authorizeNetworkInterface(ctx, s.vnicAuthorization, path, "GET")
}
