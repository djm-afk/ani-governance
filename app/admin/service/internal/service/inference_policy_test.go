package service

import (
	"context"
	"testing"

	runtimev1 "github.com/liangzai006/ani-inference-service/api/inference/v1"
	"github.com/stretchr/testify/require"
	view "go-wind-admin/api/gen/go/inference/service/v1"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type policyOwnerProbe struct {
	directInferenceOwnerProbe
	tenant, actor string
	request       *runtimev1.UpdateServicePolicyRequest
	calls         int
}

func (p *policyOwnerProbe) GetServicePolicyTrusted(_ context.Context, tenant, actor string, in *runtimev1.GetServicePolicyRequest) (*runtimev1.GetServicePolicyResponse, error) {
	p.calls++
	p.tenant, p.actor = tenant, actor
	return &runtimev1.GetServicePolicyResponse{Policy: &runtimev1.ServicePolicy{ResourceId: in.ResourceId, UpdatedAt: timestamppb.Now()}}, nil
}
func (p *policyOwnerProbe) UpdateServicePolicyTrusted(_ context.Context, tenant, actor string, in *runtimev1.UpdateServicePolicyRequest) (*runtimev1.UpdateServicePolicyResponse, error) {
	p.calls++
	p.tenant, p.actor, p.request = tenant, actor, in
	in.Policy.UpdatedAt = timestamppb.Now()
	return &runtimev1.UpdateServicePolicyResponse{Policy: in.Policy}, nil
}
func TestInferencePolicyTrustedScope(t *testing.T) {
	owner := &policyOwnerProbe{}
	s := NewInferenceServiceWithOwner(nil, owner, directInferenceTenantProbe{})
	id := "22222222-2222-4222-8222-222222222222"
	_, err := s.GetServicePolicy(context.Background(), &view.GetServicePolicyRequest{ResourceId: id})
	require.Equal(t, codes.Unauthenticated, status.Code(err))
	require.Zero(t, owner.calls)
	ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, TenantID: 26, ID: 33})
	out, err := s.UpdateServicePolicy(ctx, &view.UpdateServicePolicyRequest{ResourceId: id, Policy: &view.ServicePolicy{AllowedTenants: []string{}, RateLimits: map[string]*view.ServiceRateLimit{}}})
	require.NoError(t, err)
	require.Equal(t, id, out.Policy.ResourceId)
	require.Equal(t, "11111111-1111-4111-8111-111111111111", owner.tenant)
	require.Equal(t, "governance:user:33", owner.actor)
	require.Equal(t, id, owner.request.Policy.ResourceId)
	_, err = s.UpdateServicePolicy(ctx, &view.UpdateServicePolicyRequest{ResourceId: id, Policy: &view.ServicePolicy{ResourceId: "different"}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Equal(t, 1, owner.calls)
	_, err = s.UpdateServicePolicy(ctx, &view.UpdateServicePolicyRequest{ResourceId: id, Policy: &view.ServicePolicy{UpdatedAt: timestamppb.Now()}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = s.GetServicePolicy(ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = s.GetServicePolicy(auth.NewPrincipalContext(ctx, &auth.Principal{Type: auth.SubjectUser, ID: 33}), &view.GetServicePolicyRequest{ResourceId: id})
	require.Equal(t, codes.PermissionDenied, status.Code(err))
}
