package data

import (
	"context"
	runtimev1 "github.com/liangzai006/ani-inference-service/api/inference/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (c *InferenceClient) GetServicePolicyTrusted(ctx context.Context, tenant, actor string, in *runtimev1.GetServicePolicyRequest) (*runtimev1.GetServicePolicyResponse, error) {
	call, cancel, err := c.trustedContext(ctx, tenant, actor)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}
	defer cancel()
	return c.client.GetServicePolicy(call, in)
}
func (c *InferenceClient) UpdateServicePolicyTrusted(ctx context.Context, tenant, actor string, in *runtimev1.UpdateServicePolicyRequest) (*runtimev1.UpdateServicePolicyResponse, error) {
	call, cancel, err := c.trustedContext(ctx, tenant, actor)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, err.Error())
	}
	defer cancel()
	return c.client.UpdateServicePolicy(call, in)
}
