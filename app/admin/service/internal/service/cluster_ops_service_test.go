package service

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/stretchr/testify/require"
	clusteropsv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/clusterops/v1"
	authv1 "go-wind-admin/api/gen/go/authentication/service/v1"
	catalogv1 "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type clusterOpsServiceBoundary struct {
	method, actor string
	request       proto.Message
	response      proto.Message
	err           error
	calls         int
}

func clusterOpsBoundaryReply[T proto.Message](c *clusterOpsServiceBoundary, method, actor string, request proto.Message) (T, error) {
	c.method, c.actor, c.request = method, actor, request
	c.calls++
	var zero T
	if c.err != nil || c.response == nil {
		return zero, c.err
	}
	return c.response.(T), nil
}

func (c *clusterOpsServiceBoundary) ListClusters(_ context.Context, actor string, request *emptypb.Empty) (*clusteropsv1.ListClustersResponse, error) {
	return clusterOpsBoundaryReply[*clusteropsv1.ListClustersResponse](c, "ListClusters", actor, request)
}
func (c *clusterOpsServiceBoundary) GetClusterOverview(_ context.Context, actor string, request *clusteropsv1.GetClusterOverviewRequest) (*clusteropsv1.ClusterOverview, error) {
	return clusterOpsBoundaryReply[*clusteropsv1.ClusterOverview](c, "GetClusterOverview", actor, request)
}
func (c *clusterOpsServiceBoundary) ListResourceTypes(_ context.Context, actor string, request *clusteropsv1.ListResourceTypesRequest) (*clusteropsv1.ListResourceTypesResponse, error) {
	return clusterOpsBoundaryReply[*clusteropsv1.ListResourceTypesResponse](c, "ListResourceTypes", actor, request)
}
func (c *clusterOpsServiceBoundary) ListResources(_ context.Context, actor string, request *clusteropsv1.ListResourcesRequest) (*clusteropsv1.ListResourcesResponse, error) {
	return clusterOpsBoundaryReply[*clusteropsv1.ListResourcesResponse](c, "ListResources", actor, request)
}
func (c *clusterOpsServiceBoundary) GetResource(_ context.Context, actor string, request *clusteropsv1.GetResourceRequest) (*clusteropsv1.GetResourceResponse, error) {
	return clusterOpsBoundaryReply[*clusteropsv1.GetResourceResponse](c, "GetResource", actor, request)
}
func (c *clusterOpsServiceBoundary) CreateResource(_ context.Context, actor string, request *clusteropsv1.CreateResourceRequest) (*clusteropsv1.MutationResponse, error) {
	return clusterOpsBoundaryReply[*clusteropsv1.MutationResponse](c, "CreateResource", actor, request)
}
func (c *clusterOpsServiceBoundary) UpdateResource(_ context.Context, actor string, request *clusteropsv1.UpdateResourceRequest) (*clusteropsv1.MutationResponse, error) {
	return clusterOpsBoundaryReply[*clusteropsv1.MutationResponse](c, "UpdateResource", actor, request)
}
func (c *clusterOpsServiceBoundary) DeleteResource(_ context.Context, actor string, request *clusteropsv1.DeleteResourceRequest) (*clusteropsv1.DeleteResourceResponse, error) {
	return clusterOpsBoundaryReply[*clusteropsv1.DeleteResourceResponse](c, "DeleteResource", actor, request)
}
func (c *clusterOpsServiceBoundary) ScaleWorkload(_ context.Context, actor string, request *clusteropsv1.ScaleWorkloadRequest) (*clusteropsv1.ScaleWorkloadResponse, error) {
	return clusterOpsBoundaryReply[*clusteropsv1.ScaleWorkloadResponse](c, "ScaleWorkload", actor, request)
}
func (c *clusterOpsServiceBoundary) RestartWorkload(_ context.Context, actor string, request *clusteropsv1.RestartWorkloadRequest) (*clusteropsv1.MutationResponse, error) {
	return clusterOpsBoundaryReply[*clusteropsv1.MutationResponse](c, "RestartWorkload", actor, request)
}

type clusterOpsBoundaryStream struct{ grpc.ClientStream }

func (*clusterOpsBoundaryStream) Recv() (*clusteropsv1.PodLogFrame, error) { return nil, io.EOF }
func (c *clusterOpsServiceBoundary) StreamPodLogs(_ context.Context, actor string, request *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
	c.method, c.actor, c.request = "StreamPodLogs", actor, request
	c.calls++
	if c.err != nil {
		return nil, c.err
	}
	return &clusterOpsBoundaryStream{}, nil
}

func clusterOpsServiceContext() context.Context {
	return auth.NewContext(context.Background(), &authv1.UserTokenPayload{
		UserId: 9, Roles: []string{"platform:admin"}, Permissions: []string{
			"cluster:ops:read", "cluster:ops:create", "cluster:ops:update", "cluster:ops:delete",
			"cluster:ops:scale", "cluster:ops:restart", "cluster:ops:logs",
		},
	})
}

func TestClusterOpsServicePlatformIdentity(t *testing.T) {
	claims := &authv1.UserTokenPayload{UserId: 9, Roles: []string{"platform:reader"}, Permissions: []string{"cluster:ops:read"}}
	s := NewPlatformClusterOpsService(&clusterOpsServiceBoundary{})
	actor, err := s.actor(auth.NewContext(context.Background(), claims), "ListClusters")
	require.NoError(t, err)
	require.Equal(t, "governance:user:9", actor)
	_, err = NewPlatformClusterOpsService(nil).actor(auth.NewContext(context.Background(), claims), "ListClusters")
	require.Equal(t, "CLUSTEROPS_UNAVAILABLE", errors.FromError(err).Reason)
	_, err = s.actor(context.Background(), "ListClusters")
	require.Equal(t, int32(401), errors.FromError(err).Code)
	for _, tc := range []struct {
		name      string
		principal *auth.Principal
		method    string
	}{
		{"api_key", &auth.Principal{Type: auth.SubjectAPIKey, ID: 9, Claims: claims, Roles: claims.Roles}, "ListClusters"},
		{"tenant", &auth.Principal{Type: auth.SubjectUser, ID: 9, TenantID: 1, Claims: claims, Roles: claims.Roles}, "ListClusters"},
		{"claims_tenant", &auth.Principal{Type: auth.SubjectUser, ID: 9, Claims: &authv1.UserTokenPayload{UserId: 9, TenantId: proto.Uint32(1), Permissions: claims.Permissions}, Roles: claims.Roles}, "ListClusters"},
		{"identity_mismatch", &auth.Principal{Type: auth.SubjectUser, ID: 8, Claims: claims, Roles: claims.Roles}, "ListClusters"},
		{"no_claims", &auth.Principal{Type: auth.SubjectUser, ID: 9, Roles: claims.Roles}, "ListClusters"},
		{"no_platform_role", &auth.Principal{Type: auth.SubjectUser, ID: 9, Claims: claims, Roles: []string{"tenant:admin"}}, "ListClusters"},
		{"read_not_write", &auth.Principal{Type: auth.SubjectUser, ID: 9, Claims: claims, Roles: claims.Roles}, "UpdateResource"},
		{"read_not_logs", &auth.Principal{Type: auth.SubjectUser, ID: 9, Claims: claims, Roles: claims.Roles}, "StreamPodLogs"},
		{"unknown_method", &auth.Principal{Type: auth.SubjectUser, ID: 9, Claims: claims, Roles: claims.Roles}, "Unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.actor(auth.NewPrincipalContext(context.Background(), tc.principal), tc.method)
			require.Equal(t, int32(403), errors.FromError(err).Code)
			require.Equal(t, "PLATFORM_CLUSTEROPS_DENIED", errors.FromError(err).Reason)
		})
	}
}

func TestClusterOpsServicePreservesNativeDocumentCursorAndZero(t *testing.T) {
	ctx := clusterOpsServiceContext()
	boundary := &clusterOpsServiceBoundary{}
	s := NewPlatformClusterOpsService(boundary)
	typ := &catalogv1.ClusterOpsResourceTypeRef{Group: "apps", Version: "v1", Resource: "deployments"}
	ref := &clusteropsv1.ResourceRef{ClusterId: "cluster", Type: toClusterOpsResourceType(typ), Namespace: "ns", Name: "object"}
	const rawJSON = "{\"integer\":9007199254740993,\"text\":\"中文\\n\\n重复\\n重复\\n\"}"
	rawYAML := "apiVersion: example/v1\nkind: Example\nspec:\n  integer: 9007199254740993\n  padding: " + strings.Repeat("x", 70000) + "\n"
	doc := &clusteropsv1.ResourceDocument{
		Ref: ref, Uid: "native-uid", ResourceVersion: "18446744073709551615", ObjectJson: rawJSON,
		ReadOnly: true, ReadOnlyReason: "TENANT_NAMESPACE_READ_ONLY", SecretDataRedacted: true, SecretKeys: []string{"token"},
	}
	boundary.response = &clusteropsv1.ListClustersResponse{Items: []*clusteropsv1.Cluster{{ClusterId: "cluster", DisplayName: "真实集群"}}}
	clusters, err := s.ListClusters(ctx, &catalogv1.ClusterOpsListClustersRequest{})
	require.NoError(t, err)
	require.Equal(t, "真实集群", clusters.Items[0].DisplayName)
	require.Equal(t, "governance:user:9", boundary.actor)

	boundary.response = &clusteropsv1.ClusterOverview{
		ClusterId: "cluster", KubernetesVersion: "v1.34", NodesTotal: 9007199254740993, NodesReady: 2,
		NamespacesTotal: 3, PodsTotal: 4, PodPhaseCounts: map[string]int64{"Running": 9007199254740993},
		Capacity: map[string]string{"memory": "123Ki"}, Allocatable: map[string]string{"nvidia.com/gpu": "4"},
	}
	overview, err := s.GetClusterOverview(ctx, &catalogv1.ClusterOpsGetClusterOverviewRequest{ClusterId: "cluster"})
	require.NoError(t, err)
	require.Equal(t, int64(9007199254740993), overview.NodesTotal)
	body, err := protojson.Marshal(overview)
	require.NoError(t, err)
	require.Contains(t, string(body), "\"9007199254740993\"")
	require.Equal(t, "123Ki", overview.Capacity["memory"])
	require.Equal(t, "4", overview.Allocatable["nvidia.com/gpu"])

	boundary.response = &clusteropsv1.ListResourceTypesResponse{Items: []*clusteropsv1.ResourceDescriptor{{
		Type: ref.Type, Kind: "Deployment", Namespaced: true, Actions: []string{"list", "scale"}, CustomResource: true, CrdName: "objects.example",
	}}}
	types, err := s.ListResourceTypes(ctx, &catalogv1.ClusterOpsListResourceTypesRequest{ClusterId: "cluster"})
	require.NoError(t, err)
	require.True(t, types.Items[0].CustomResource)
	require.Equal(t, "objects.example", types.Items[0].CrdName)
	require.Equal(t, []string{"list", "scale"}, types.Items[0].Actions)

	boundary.response = &clusteropsv1.ListResourcesResponse{Items: []*clusteropsv1.ResourceDocument{doc}, NextContinueToken: "opaque/%+next", ListResourceVersion: "9007199254740995"}
	list, err := s.ListResources(ctx, &catalogv1.ClusterOpsListResourcesRequest{
		ClusterId: "cluster", Group: typ.Group, Version: typ.Version, Resource: typ.Resource, Namespace: "ns",
		ContinueToken: "opaque/%+previous", Name: "object", LabelSelector: "app in (one,two)", FieldSelector: "metadata.name=object",
	})
	require.NoError(t, err)
	sentList := boundary.request.(*clusteropsv1.ListResourcesRequest)
	require.Zero(t, sentList.PageSize, "omission uses Resource's own default")
	require.Equal(t, "opaque/%+previous", sentList.ContinueToken)
	require.Equal(t, "app in (one,two)", sentList.LabelSelector)
	require.Equal(t, "metadata.name=object", sentList.FieldSelector)
	require.Equal(t, "opaque/%+next", list.NextContinueToken)
	require.Equal(t, "9007199254740995", list.ListResourceVersion)
	require.Equal(t, rawJSON, list.Items[0].ObjectJson)
	require.Equal(t, doc.ResourceVersion, list.Items[0].ResourceVersion)
	require.True(t, list.Items[0].ReadOnly)
	require.Equal(t, doc.ReadOnlyReason, list.Items[0].ReadOnlyReason)
	require.True(t, list.Items[0].SecretDataRedacted)
	require.Equal(t, []string{"token"}, list.Items[0].SecretKeys)

	boundary.response = &clusteropsv1.GetResourceResponse{Resource: doc, ManifestYaml: rawYAML}
	detail, err := s.GetResource(ctx, &catalogv1.ClusterOpsGetResourceRequest{
		ClusterId: "cluster", Name: "object", Group: typ.Group, Version: typ.Version, Resource: typ.Resource, Namespace: "ns", IncludeSecretData: true,
	})
	require.NoError(t, err)
	require.True(t, boundary.request.(*clusteropsv1.GetResourceRequest).IncludeSecretData)
	require.Equal(t, rawYAML, detail.ManifestYaml)
	require.Equal(t, rawJSON, detail.Resource.ObjectJson)

	boundary.response = &clusteropsv1.MutationResponse{Resource: doc, DryRun: true}
	created, err := s.CreateResource(ctx, &catalogv1.ClusterOpsCreateResourceRequest{ClusterId: "cluster", Data: &catalogv1.ClusterOpsCreateResourceData{
		Type: typ, Namespace: "ns", Name: "object", ManifestYaml: rawYAML, DryRun: true,
	}})
	require.NoError(t, err)
	require.Equal(t, rawYAML, boundary.request.(*clusteropsv1.CreateResourceRequest).ManifestYaml)
	require.True(t, created.DryRun)
	updated, err := s.UpdateResource(ctx, &catalogv1.ClusterOpsUpdateResourceRequest{ClusterId: "cluster", Name: "object", Data: &catalogv1.ClusterOpsUpdateResourceData{
		Type: typ, Namespace: "ns", ManifestYaml: rawYAML, ExpectedUid: doc.Uid, ExpectedResourceVersion: doc.ResourceVersion, DryRun: true,
	}})
	require.NoError(t, err)
	sentUpdate := boundary.request.(*clusteropsv1.UpdateResourceRequest)
	require.Equal(t, rawYAML, sentUpdate.ManifestYaml)
	require.Equal(t, doc.Uid, sentUpdate.ExpectedUid)
	require.Equal(t, doc.ResourceVersion, sentUpdate.ExpectedResourceVersion)
	require.True(t, updated.DryRun)

	boundary.response = &clusteropsv1.ScaleWorkloadResponse{Ref: ref, Uid: doc.Uid, ResourceVersion: doc.ResourceVersion, DesiredReplicas: 0, ObservedReplicas: 2, DryRun: true}
	scaled, err := s.ScaleWorkload(ctx, &catalogv1.ClusterOpsScaleWorkloadRequest{ClusterId: "cluster", Name: "object", Data: &catalogv1.ClusterOpsScaleWorkloadData{
		Type: typ, Namespace: "ns", ExpectedUid: doc.Uid, ExpectedResourceVersion: doc.ResourceVersion, Replicas: proto.Int32(0), DryRun: true,
	}})
	require.NoError(t, err)
	sentScale := boundary.request.(*clusteropsv1.ScaleWorkloadRequest)
	require.NotNil(t, sentScale.Replicas)
	require.Zero(t, *sentScale.Replicas)
	require.Equal(t, doc.Uid, sentScale.ExpectedUid)
	require.Equal(t, doc.ResourceVersion, sentScale.ExpectedResourceVersion)
	require.Equal(t, int32(2), scaled.ObservedReplicas)
	require.True(t, scaled.DryRun)

	boundary.response = &clusteropsv1.MutationResponse{Resource: doc}
	restarted, err := s.RestartWorkload(ctx, &catalogv1.ClusterOpsRestartWorkloadRequest{ClusterId: "cluster", Name: "object", Data: &catalogv1.ClusterOpsRestartWorkloadData{
		Type: typ, Namespace: "ns", ExpectedUid: doc.Uid, ExpectedResourceVersion: doc.ResourceVersion,
	}})
	require.NoError(t, err)
	require.Equal(t, doc.ResourceVersion, boundary.request.(*clusteropsv1.RestartWorkloadRequest).ExpectedResourceVersion)
	require.False(t, restarted.DryRun)

	for _, deletion := range []*clusteropsv1.DeleteResourceResponse{{DeletionRequested: true}, {AlreadyAbsent: true}} {
		boundary.response = deletion
		before := boundary.calls
		deleted, err := s.DeleteResource(ctx, &catalogv1.ClusterOpsDeleteResourceRequest{
			ClusterId: "cluster", Name: "object", Group: typ.Group, Version: typ.Version, Resource: typ.Resource, Namespace: "ns", ExpectedUid: doc.Uid,
		})
		require.NoError(t, err)
		require.Equal(t, before+1, boundary.calls, "delete result must not trigger fallback queries or repetition")
		require.Equal(t, doc.Uid, boundary.request.(*clusteropsv1.DeleteResourceRequest).ExpectedUid)
		require.Equal(t, deletion.DeletionRequested, deleted.DeletionRequested)
		require.Equal(t, deletion.AlreadyAbsent, deleted.AlreadyAbsent)
	}
	_, err = s.OpenPodLogs(ctx, &catalogv1.ClusterOpsStreamPodLogsRequest{
		ClusterId: "cluster", Namespace: "ns", PodName: "pod", Container: "main", ExpectedPodUid: "pod-uid",
		Follow: true, Previous: true, Timestamps: true, TailLines: proto.Int64(0), LimitBytes: proto.Int64(9007199254740993),
	})
	require.NoError(t, err)
	sentLogs := boundary.request.(*clusteropsv1.StreamPodLogsRequest)
	require.NotNil(t, sentLogs.TailLines)
	require.Zero(t, *sentLogs.TailLines)
	require.Equal(t, int64(9007199254740993), *sentLogs.LimitBytes)
	require.Equal(t, "pod-uid", sentLogs.ExpectedPodUid)
	require.True(t, sentLogs.Follow && sentLogs.Previous && sentLogs.Timestamps)
}

func TestClusterOpsServiceInvalidInputsAndUnavailableResponse(t *testing.T) {
	ctx := clusterOpsServiceContext()
	boundary := &clusterOpsServiceBoundary{}
	s := NewPlatformClusterOpsService(boundary)
	for _, call := range []func() error{
		func() error { _, e := s.ListClusters(ctx, nil); return e },
		func() error { _, e := s.GetClusterOverview(ctx, nil); return e },
		func() error { _, e := s.ListResourceTypes(ctx, nil); return e },
		func() error { _, e := s.ListResources(ctx, nil); return e },
		func() error { _, e := s.GetResource(ctx, nil); return e },
		func() error {
			_, e := s.CreateResource(ctx, &catalogv1.ClusterOpsCreateResourceRequest{ClusterId: "cluster"})
			return e
		},
		func() error {
			_, e := s.UpdateResource(ctx, &catalogv1.ClusterOpsUpdateResourceRequest{ClusterId: "cluster"})
			return e
		},
		func() error { _, e := s.DeleteResource(ctx, nil); return e },
		func() error {
			_, e := s.ScaleWorkload(ctx, &catalogv1.ClusterOpsScaleWorkloadRequest{ClusterId: "cluster", Name: "d", Data: &catalogv1.ClusterOpsScaleWorkloadData{Type: &catalogv1.ClusterOpsResourceTypeRef{Group: "apps", Version: "v1", Resource: "deployments"}, ExpectedUid: "uid", ExpectedResourceVersion: "rv"}})
			return e
		},
		func() error {
			_, e := s.RestartWorkload(ctx, &catalogv1.ClusterOpsRestartWorkloadRequest{ClusterId: "cluster"})
			return e
		},
		func() error {
			_, e := s.OpenPodLogs(ctx, &catalogv1.ClusterOpsStreamPodLogsRequest{ClusterId: "cluster", Namespace: "ns", PodName: "pod", TailLines: proto.Int64(-1)})
			return e
		},
	} {
		require.Equal(t, "INVALID_CLUSTEROPS_REQUEST", errors.FromError(call()).Reason)
	}
	require.Zero(t, boundary.calls)
	_, err := s.ListClusters(ctx, &catalogv1.ClusterOpsListClustersRequest{})
	require.Equal(t, "CLUSTEROPS_UPSTREAM_ERROR", errors.FromError(err).Reason)
	boundary.response = &clusteropsv1.ListClustersResponse{Items: []*clusteropsv1.Cluster{nil}}
	_, err = s.ListClusters(ctx, &catalogv1.ClusterOpsListClustersRequest{})
	require.Equal(t, "CLUSTEROPS_UPSTREAM_ERROR", errors.FromError(err).Reason)
}

func TestClusterOpsServiceDoesNotReplayUncertainMutation(t *testing.T) {
	boundary := &clusterOpsServiceBoundary{err: status.Error(codes.DeadlineExceeded, "private-upstream-body")}
	s := NewPlatformClusterOpsService(boundary)
	draft := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: example\ndata:\n  value: original\n"
	_, err := s.UpdateResource(clusterOpsServiceContext(), &catalogv1.ClusterOpsUpdateResourceRequest{
		ClusterId: "cluster", Name: "example", Data: &catalogv1.ClusterOpsUpdateResourceData{
			Type:      &catalogv1.ClusterOpsResourceTypeRef{Version: "v1", Resource: "configmaps"},
			Namespace: "ns", ManifestYaml: draft, ExpectedUid: "original-uid", ExpectedResourceVersion: "original-rv",
		},
	})
	require.Equal(t, int32(504), errors.FromError(err).Code)
	require.Equal(t, 1, boundary.calls, "an uncertain write never triggers a retry or verification query")
	sent := boundary.request.(*clusteropsv1.UpdateResourceRequest)
	require.Equal(t, draft, sent.ManifestYaml)
	require.Equal(t, "original-uid", sent.ExpectedUid)
	require.Equal(t, "original-rv", sent.ExpectedResourceVersion)
	require.NotContains(t, err.Error(), "private-upstream-body")
}
