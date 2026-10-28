package data

import (
	"context"

	runtimev1 "github.com/liangzai006/ani-inference-service/api/inference/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Key management shares the managed mTLS connection, never the platform
// AccessKeyRepo or the data-plane CheckInferenceAccess RPC.
func (c *InferenceClient) CreateInferenceKeyTrusted(ctx context.Context, tenant, actor string, in *runtimev1.CreateInferenceAccessKeyRequest) (*runtimev1.CreateInferenceAccessKeyResponse, error) {
	call, cancel, err := c.trustedKeyContext(ctx, tenant, actor)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.keys.CreateInferenceAccessKey(call, in)
}

func (c *InferenceClient) ListInferenceKeysTrusted(ctx context.Context, tenant, actor string, in *runtimev1.ListInferenceAccessKeysRequest) (*runtimev1.ListInferenceAccessKeysResponse, error) {
	call, cancel, err := c.trustedKeyContext(ctx, tenant, actor)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.keys.ListInferenceAccessKeys(call, in)
}

func (c *InferenceClient) GetInferenceKeyTrusted(ctx context.Context, tenant, actor string, in *runtimev1.GetInferenceAccessKeyRequest) (*runtimev1.InferenceAccessKey, error) {
	call, cancel, err := c.trustedKeyContext(ctx, tenant, actor)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.keys.GetInferenceAccessKey(call, in)
}

func (c *InferenceClient) RevokeInferenceKeyTrusted(ctx context.Context, tenant, actor string, in *runtimev1.RevokeInferenceAccessKeyRequest) (*runtimev1.RevokeInferenceAccessKeyResponse, error) {
	call, cancel, err := c.trustedKeyContext(ctx, tenant, actor)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.keys.RevokeInferenceAccessKey(call, in)
}

func (c *InferenceClient) RotateInferenceKeyTrusted(ctx context.Context, tenant, actor string, in *runtimev1.RotateInferenceAccessKeyRequest) (*runtimev1.RotateInferenceAccessKeyResponse, error) {
	call, cancel, err := c.trustedKeyContext(ctx, tenant, actor)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return c.keys.RotateInferenceAccessKey(call, in)
}

func (c *InferenceClient) trustedKeyContext(ctx context.Context, tenant, actor string) (context.Context, context.CancelFunc, error) {
	if c == nil || c.keys == nil {
		return nil, nil, status.Error(codes.Unavailable, "inference key client unavailable")
	}
	call, cancel, err := c.trustedContext(ctx, tenant, actor)
	if err != nil {
		return nil, nil, status.Error(codes.Unauthenticated, err.Error())
	}
	return call, cancel, nil
}
