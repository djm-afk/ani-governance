package service

import (
	"context"

	"github.com/go-kratos/kratos/v2/errors"
	networkv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	catalogv1 "go-wind-admin/api/gen/go/catalog/service/v1"
)

// ListNetworkInstances exposes the current tenant's active attachment summaries.
// Resource owns network visibility, parent/subnet pairing and consumer lookup.
func (s *NetworkService) ListNetworkInstances(ctx context.Context, req *catalogv1.ListNetworkInstancesRequest) (*catalogv1.ListNetworkInstancesResponse, error) {
	if req == nil || (req.VpcId == "" && req.SubnetId == "") ||
		(req.VpcId != "" && !vpcIDPattern.MatchString(req.VpcId)) ||
		(req.SubnetId != "" && !subnetIDPattern.MatchString(req.SubnetId)) {
		return nil, errors.BadRequest("INVALID_NETWORK_REQUEST", "at least one valid VPC or subnet ID is required")
	}
	page, pageSize, err := resolveVPCPage(req.Page, req.PageSize)
	if err != nil {
		return nil, err
	}
	operator, tenant, err := trustedOperator(ctx, s)
	if err != nil {
		return nil, err
	}
	actor, err := operator.Actor()
	if err != nil {
		return nil, err
	}
	reply, err := s.client.ListNetworkInstances(ctx, tenant, actor, req.VpcId, req.SubnetId, page, pageSize)
	if err != nil {
		return nil, mapNetworkError(err)
	}
	if reply == nil || reply.Total < 0 || len(reply.Items) > int(pageSize) || int64(len(reply.Items)) > reply.Total {
		return nil, invalidNetworkReply()
	}
	out := &catalogv1.ListNetworkInstancesResponse{Total: reply.Total, Items: make([]*catalogv1.NetworkAssociatedInstance, 0, len(reply.Items))}
	for _, item := range reply.Items {
		// A platform-owned network never changes the attachment's true tenant.
		if item == nil || item.TenantId != tenant {
			return nil, invalidNetworkReply()
		}
		out.Items = append(out.Items, wireNetworkAssociatedInstance(item))
	}
	return out, nil
}

func wireNetworkAssociatedInstance(v *networkv1.AssociatedInstance) *catalogv1.NetworkAssociatedInstance {
	out := &catalogv1.NetworkAssociatedInstance{
		ConsumerType: v.ConsumerType, ConsumerId: v.ConsumerId, ConsumerName: v.ConsumerName, ConsumerState: v.ConsumerState,
		SubnetId: v.SubnetId, SubnetName: v.SubnetName, Slot: v.Slot, AttachmentId: v.AttachmentId,
		AttachmentState: v.AttachmentState, AttachmentReason: v.AttachmentReason,
		ObservationStale: v.ObservationStale, ObservedAt: v.ObservedAt,
		Addresses: make([]*catalogv1.NetworkInstanceAddress, 0, len(v.Addresses)),
	}
	for _, address := range v.Addresses {
		out.Addresses = append(out.Addresses, &catalogv1.NetworkInstanceAddress{NicName: address.GetNicName(), Address: address.GetAddress()})
	}
	return out
}
