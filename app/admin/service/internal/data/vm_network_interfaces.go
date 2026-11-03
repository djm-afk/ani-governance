package data

import (
	"context"
	network "github.com/zhangzhe-ctrl/ani-resource-service/api/network/v1"
	vm "github.com/zhangzhe-ctrl/ani-resource-service/api/vm/v1"
)

func (c *VMClient) GetNetworkInterfaces(ctx context.Context, t, a, id string) (*network.NetworkInterfaces, error) {
	return vmCall(c, ctx, t, a, func(ctx context.Context) (*network.NetworkInterfaces, error) {
		return c.client.GetNetworkInterfaces(ctx, &vm.GetVMRequest{TenantId: t, VmId: id})
	})
}
