package data

import (
	"context"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	network "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func (c *InstanceClient) GetNetworkInterfaces(ctx context.Context, t, a, id string) (*network.NetworkInterfaces, error) {
	return instanceCall(c, ctx, t, a, func(ctx context.Context) (*network.NetworkInterfaces, error) {
		return c.client.GetNetworkInterfaces(ctx, &res.GetInstanceRequest{TenantId: t, InstanceId: id})
	})
}
func (c *InstanceClient) SetNetworkInterface(ctx context.Context, t, a string, request *res.SetNetworkInterfaceRequest) (*res.OperationResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "network interface request required")
	}
	in := proto.Clone(request).(*res.SetNetworkInterfaceRequest)
	in.TenantId = t
	return instanceCall(c, ctx, t, a, func(ctx context.Context) (*res.OperationResponse, error) {
		return c.client.SetNetworkInterface(ctx, in)
	})
}
