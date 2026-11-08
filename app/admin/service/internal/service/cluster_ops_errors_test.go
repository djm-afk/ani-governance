package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestClusterOpsErrorDomainAndSensitiveData(t *testing.T) {
	for _, tc := range []struct {
		reason string
		code   int32
	}{
		{"INVALID_ARGUMENT", 400}, {"SYSTEM_FIELD_READ_ONLY", 400}, {"KUBERNETES_INVALID", 400}, {"POD_CONTAINER_REQUIRED", 400},
		{"RESOURCE_READ_ONLY", 403}, {"TENANT_NAMESPACE_READ_ONLY", 403}, {"KUBERNETES_FORBIDDEN", 403},
		{"CLUSTER_NOT_CONFIGURED", 404}, {"RESOURCE_NOT_FOUND", 404},
		{"RESOURCE_ALREADY_EXISTS", 409}, {"RESOURCE_UID_CHANGED", 409}, {"RESOURCE_VERSION_CONFLICT", 409},
		{"POD_CHANGED", 409}, {"RESOURCE_TYPE_UNAVAILABLE", 409}, {"ACTION_NOT_SUPPORTED", 409},
		{"RESTART_STRATEGY_UNSUPPORTED", 409}, {"CRD_HAS_PROTECTED_NAMESPACE_INSTANCES", 409}, {"CRD_INSPECTION_FAILED", 409}, {"LIST_EXPIRED", 409},
		{"RESOURCE_EXHAUSTED", 429}, {"KUBERNETES_UNAUTHORIZED", 502}, {"PLATFORM_SCOPE_REQUIRED", 502},
		{"NAMESPACE_POLICY_UNAVAILABLE", 503}, {"KUBERNETES_UNAVAILABLE", 503}, {"LOG_READ_FAILED", 503},
		{"LOG_SEND_TIMEOUT", 504}, {"INTERNAL_ERROR", 500},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			upstream, err := status.New(codes.Unknown, "private-manifest-secret-sentinel").WithDetails(&errdetails.ErrorInfo{
				Domain: "clusterops.ani.io", Reason: tc.reason,
				Metadata: map[string]string{
					"cluster_id": "cluster", "namespace": "ns", "name": "object",
					"kubernetes_http_status": "403", "field_paths": "spec.replicas",
					"request_id": "forged", "token": "private-token-sentinel", "manifest_yaml": "private-manifest-secret-sentinel",
				},
			})
			require.NoError(t, err)
			public := errors.FromError(mapClusterOpsError(upstream.Err()))
			require.Equal(t, tc.code, public.Code)
			require.Equal(t, tc.reason, public.Reason)
			require.Equal(t, map[string]string{
				"cluster_id": "cluster", "namespace": "ns", "name": "object",
				"kubernetes_http_status": "403", "field_paths": "spec.replicas",
			}, public.Metadata)
			require.NotContains(t, public.Error(), "sentinel")
			frame := errors.FromError(ClusterOpsLogFrameError(tc.reason))
			require.Equal(t, tc.code, frame.Code)
			require.Equal(t, public.Message, frame.Message)
		})
	}
	for _, domain := range []string{"", "other.ani.io"} {
		upstream, err := status.New(codes.Unauthenticated, "private-certificate-sentinel").WithDetails(&errdetails.ErrorInfo{
			Domain: domain, Reason: "KUBERNETES_UNAUTHORIZED", Metadata: map[string]string{"cluster_id": "private"},
		})
		require.NoError(t, err)
		public := errors.FromError(MapClusterOpsError(upstream.Err()))
		require.Equal(t, int32(502), public.Code, "upstream auth failures are never browser 401")
		require.Equal(t, "CLUSTEROPS_UPSTREAM_AUTH_FAILED", public.Reason)
		require.Empty(t, public.Metadata)
		require.NotContains(t, public.Error(), "sentinel")
	}
	unknown, err := status.New(codes.Unknown, "private-sentinel").WithDetails(&errdetails.ErrorInfo{Domain: "clusterops.ani.io", Reason: "NEW_UNKNOWN_REASON"})
	require.NoError(t, err)
	require.Equal(t, "CLUSTEROPS_UPSTREAM_ERROR", errors.FromError(mapClusterOpsError(unknown.Err())).Reason)
	require.Equal(t, "CLUSTEROPS_UPSTREAM_ERROR", errors.FromError(ClusterOpsLogFrameError("NEW_UNKNOWN_REASON")).Reason)
	ambiguous, err := status.New(codes.Unknown, "private-sentinel").WithDetails(
		&errdetails.ErrorInfo{Domain: "clusterops.ani.io", Reason: "RESOURCE_NOT_FOUND"},
		&errdetails.ErrorInfo{Domain: "clusterops.ani.io", Reason: "KUBERNETES_UNAUTHORIZED"},
	)
	require.NoError(t, err)
	require.Equal(t, "CLUSTEROPS_UPSTREAM_ERROR", errors.FromError(mapClusterOpsError(ambiguous.Err())).Reason)
}

func TestClusterOpsErrorTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		err    error
		code   int32
		reason string
	}{
		{status.Error(codes.Unimplemented, "private"), 503, "CLUSTEROPS_UNAVAILABLE"},
		{status.Error(codes.Unauthenticated, "private"), 502, "CLUSTEROPS_UPSTREAM_AUTH_FAILED"},
		{status.Error(codes.PermissionDenied, "private"), 502, "CLUSTEROPS_UPSTREAM_AUTH_FAILED"},
		{status.Error(codes.DeadlineExceeded, "private"), 504, "DEADLINE_EXCEEDED"},
		{fmt.Errorf("deadline: %w", context.DeadlineExceeded), 504, "DEADLINE_EXCEEDED"},
		{status.Error(codes.ResourceExhausted, "private"), 502, "CLUSTEROPS_UPSTREAM_ERROR"},
		{status.Error(codes.Unavailable, "private"), 502, "CLUSTEROPS_UPSTREAM_ERROR"},
		{fmt.Errorf("private body"), 502, "CLUSTEROPS_UPSTREAM_ERROR"},
	} {
		public := errors.FromError(mapClusterOpsError(tc.err))
		require.Equal(t, tc.code, public.Code)
		require.Equal(t, tc.reason, public.Reason)
		require.NotContains(t, public.Error(), "private")
	}
	require.Nil(t, mapClusterOpsError(nil))
	require.ErrorIs(t, mapClusterOpsError(context.Canceled), context.Canceled)
	require.ErrorIs(t, mapClusterOpsError(status.Error(codes.Canceled, "private")), context.Canceled)
}

func TestClusterOpsResourceExhaustedRequiresTrustedDomain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		info   *errdetails.ErrorInfo
		code   int32
		reason string
	}{
		{name: "receive limit without domain", code: 502, reason: "CLUSTEROPS_UPSTREAM_ERROR"},
		{name: "foreign domain", info: &errdetails.ErrorInfo{Domain: "other.ani.io", Reason: "RESOURCE_EXHAUSTED"}, code: 502, reason: "CLUSTEROPS_UPSTREAM_ERROR"},
		{name: "missing domain", info: &errdetails.ErrorInfo{Reason: "RESOURCE_EXHAUSTED"}, code: 502, reason: "CLUSTEROPS_UPSTREAM_ERROR"},
		{name: "trusted domain throttle", info: &errdetails.ErrorInfo{Domain: "clusterops.ani.io", Reason: "RESOURCE_EXHAUSTED"}, code: 429, reason: "RESOURCE_EXHAUSTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rpc := status.New(codes.ResourceExhausted, "grpc: received message larger than max (4609000 vs. 4194304); private-body-sentinel")
			if tc.info != nil {
				var err error
				rpc, err = rpc.WithDetails(tc.info)
				require.NoError(t, err)
			}
			public := errors.FromError(mapClusterOpsError(rpc.Err()))
			require.Equal(t, tc.code, public.Code)
			require.Equal(t, tc.reason, public.Reason)
			require.Empty(t, public.Metadata)
			require.NotContains(t, public.Error(), "private-body-sentinel")
			require.NotContains(t, public.Message, "received message")
		})
	}
}
