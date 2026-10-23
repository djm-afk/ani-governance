package service

import (
	"context"

	"github.com/google/uuid"
	runtimev1 "github.com/liangzai006/ani-inference-service/api/inference/v1"
	view "go-wind-admin/api/gen/go/inference/service/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type inferencePolicyOwner interface {
	GetServicePolicyTrusted(context.Context, string, string, *runtimev1.GetServicePolicyRequest) (*runtimev1.GetServicePolicyResponse, error)
	UpdateServicePolicyTrusted(context.Context, string, string, *runtimev1.UpdateServicePolicyRequest) (*runtimev1.UpdateServicePolicyResponse, error)
}

func (s *InferenceService) policyScope(ctx context.Context, id string) (inferencePolicyOwner, string, string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return nil, "", "", status.Error(codes.InvalidArgument, "resource_id must be a canonical UUID")
	}
	tenant, actor, err := s.trustedInferenceOperator(ctx)
	if err != nil {
		return nil, "", "", err
	}
	owner, ok := s.owner.(inferencePolicyOwner)
	if !ok {
		return nil, "", "", status.Error(codes.Unavailable, "INFERENCE_POLICY_UNAVAILABLE")
	}
	return owner, tenant, actor, nil
}
func (s *InferenceService) GetServicePolicy(ctx context.Context, in *view.GetServicePolicyRequest) (*view.GetServicePolicyResponse, error) {
	owner, tenant, actor, err := s.policyScope(ctx, in.GetResourceId())
	if err != nil {
		return nil, err
	}
	result, err := owner.GetServicePolicyTrusted(ctx, tenant, actor, &runtimev1.GetServicePolicyRequest{ResourceId: in.ResourceId})
	if err != nil {
		return nil, mapInferencePolicyError(err)
	}
	policy, err := projectServicePolicy(in.ResourceId, result.GetPolicy())
	if err != nil {
		return nil, err
	}
	return &view.GetServicePolicyResponse{Policy: policy}, nil
}
func (s *InferenceService) UpdateServicePolicy(ctx context.Context, in *view.UpdateServicePolicyRequest) (*view.UpdateServicePolicyResponse, error) {
	owner, tenant, actor, err := s.policyScope(ctx, in.GetResourceId())
	if err != nil {
		return nil, err
	}
	if in.Policy == nil || in.Policy.UpdatedAt != nil || (in.Policy.ResourceId != "" && in.Policy.ResourceId != in.ResourceId) {
		return nil, status.Error(codes.InvalidArgument, "policy is required; resource ID must match and updated_at is read-only")
	}
	p := &runtimev1.ServicePolicy{ResourceId: in.ResourceId, AllowedTenants: append([]string(nil), in.Policy.AllowedTenants...), RateLimits: map[string]*runtimev1.RateLimitConfig{}}
	for id, limit := range in.Policy.RateLimits {
		if limit == nil {
			return nil, status.Error(codes.InvalidArgument, "rate limit cannot be null")
		}
		p.RateLimits[id] = &runtimev1.RateLimitConfig{RequestsPerMinute: limit.RequestsPerMinute, RequestsPerHour: limit.RequestsPerHour}
	}
	result, err := owner.UpdateServicePolicyTrusted(ctx, tenant, actor, &runtimev1.UpdateServicePolicyRequest{ResourceId: in.ResourceId, Policy: p})
	if err != nil {
		return nil, mapInferencePolicyError(err)
	}
	policy, err := projectServicePolicy(in.ResourceId, result.GetPolicy())
	if err != nil {
		return nil, err
	}
	return &view.UpdateServicePolicyResponse{Policy: policy}, nil
}
func projectServicePolicy(id string, p *runtimev1.ServicePolicy) (*view.ServicePolicy, error) {
	if p == nil || p.ResourceId != id || p.UpdatedAt == nil || p.UpdatedAt.CheckValid() != nil {
		return nil, status.Error(codes.Unavailable, "INVALID_INFERENCE_POLICY")
	}
	out := &view.ServicePolicy{ResourceId: p.ResourceId, AllowedTenants: append([]string(nil), p.AllowedTenants...), RateLimits: map[string]*view.ServiceRateLimit{}, UpdatedAt: proto.Clone(p.UpdatedAt).(*timestamppb.Timestamp)}
	for tenant, limit := range p.RateLimits {
		if limit == nil {
			return nil, status.Error(codes.Unavailable, "INVALID_INFERENCE_POLICY")
		}
		out.RateLimits[tenant] = &view.ServiceRateLimit{RequestsPerMinute: limit.RequestsPerMinute, RequestsPerHour: limit.RequestsPerHour}
	}
	return out, nil
}
func mapInferencePolicyError(err error) error {
	switch status.Code(err) {
	case codes.InvalidArgument:
		return status.Error(codes.InvalidArgument, "invalid inference policy")
	case codes.NotFound:
		return status.Error(codes.NotFound, "inference service not found")
	case codes.DeadlineExceeded:
		return status.Error(codes.DeadlineExceeded, "inference policy request timed out")
	case codes.Canceled:
		return status.Error(codes.Canceled, "inference policy request canceled")
	default:
		return status.Error(codes.Unavailable, "inference policy unavailable")
	}
}
