package data

import (
	"context"
	network "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (c *NetworkClient) CreateVNic(ctx context.Context, tenant, actor string, request *network.CreateVNicRequest) (*network.CreateVNicResponse, error) {
	tc, err := c.trusted(tenant, actor)
	if err != nil {
		return nil, err
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "VNIC request required")
	}
	in := proto.Clone(request).(*network.CreateVNicRequest)
	in.TargetTenantId = tc.tenant
	return outCall(c, ctx, tc, func(ctx context.Context) (*network.CreateVNicResponse, error) { return c.client.CreateVNic(ctx, in) })
}
func (c *NetworkClient) GetVNic(ctx context.Context, tenant, actor string, request *network.GetVNicRequest) (*network.GetVNicResponse, error) {
	tc, err := c.trusted(tenant, actor)
	if err != nil {
		return nil, err
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "VNIC request required")
	}
	in := proto.Clone(request).(*network.GetVNicRequest)
	in.TargetTenantId = tc.tenant
	return outCall(c, ctx, tc, func(ctx context.Context) (*network.GetVNicResponse, error) { return c.client.GetVNic(ctx, in) })
}
func (c *NetworkClient) ListVNics(ctx context.Context, tenant, actor string, request *network.ListVNicsRequest) (*network.ListVNicsResponse, error) {
	tc, err := c.trusted(tenant, actor)
	if err != nil {
		return nil, err
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "VNIC request required")
	}
	in := proto.Clone(request).(*network.ListVNicsRequest)
	in.TargetTenantId = tc.tenant
	return outCall(c, ctx, tc, func(ctx context.Context) (*network.ListVNicsResponse, error) { return c.client.ListVNics(ctx, in) })
}
func (c *NetworkClient) DeleteVNic(ctx context.Context, tenant, actor string, request *network.DeleteVNicRequest) (*network.DeleteVNicResponse, error) {
	tc, err := c.trusted(tenant, actor)
	if err != nil {
		return nil, err
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "VNIC request required")
	}
	in := proto.Clone(request).(*network.DeleteVNicRequest)
	in.TargetTenantId = tc.tenant
	return outCall(c, ctx, tc, func(ctx context.Context) (*network.DeleteVNicResponse, error) { return c.client.DeleteVNic(ctx, in) })
}
func (c *NetworkClient) ListVNicBindingTargets(ctx context.Context, tenant, actor string, request *network.ListVNicBindingTargetsRequest) (*network.ListVNicBindingTargetsResponse, error) {
	tc, err := c.trusted(tenant, actor)
	if err != nil {
		return nil, err
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "VNIC request required")
	}
	in := proto.Clone(request).(*network.ListVNicBindingTargetsRequest)
	in.TargetTenantId = tc.tenant
	return outCall(c, ctx, tc, func(ctx context.Context) (*network.ListVNicBindingTargetsResponse, error) {
		return c.client.ListVNicBindingTargets(ctx, in)
	})
}
