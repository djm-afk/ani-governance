package service

import (
	"context"

	"github.com/go-kratos/kratos/v2/errors"
	clusteropsv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/clusterops/v1"
	adminv1 "go-wind-admin/api/gen/go/admin/service/v1"
	catalogv1 "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ClusterOpsPlatformClient is the published Resource boundary. There is no
// Kubernetes client, tenant resolver or resource ownership model in this BFF.
type ClusterOpsPlatformClient interface {
	ListClusters(context.Context, string, *emptypb.Empty) (*clusteropsv1.ListClustersResponse, error)
	GetClusterOverview(context.Context, string, *clusteropsv1.GetClusterOverviewRequest) (*clusteropsv1.ClusterOverview, error)
	ListResourceTypes(context.Context, string, *clusteropsv1.ListResourceTypesRequest) (*clusteropsv1.ListResourceTypesResponse, error)
	ListResources(context.Context, string, *clusteropsv1.ListResourcesRequest) (*clusteropsv1.ListResourcesResponse, error)
	GetResource(context.Context, string, *clusteropsv1.GetResourceRequest) (*clusteropsv1.GetResourceResponse, error)
	CreateResource(context.Context, string, *clusteropsv1.CreateResourceRequest) (*clusteropsv1.MutationResponse, error)
	UpdateResource(context.Context, string, *clusteropsv1.UpdateResourceRequest) (*clusteropsv1.MutationResponse, error)
	DeleteResource(context.Context, string, *clusteropsv1.DeleteResourceRequest) (*clusteropsv1.DeleteResourceResponse, error)
	ScaleWorkload(context.Context, string, *clusteropsv1.ScaleWorkloadRequest) (*clusteropsv1.ScaleWorkloadResponse, error)
	RestartWorkload(context.Context, string, *clusteropsv1.RestartWorkloadRequest) (*clusteropsv1.MutationResponse, error)
	StreamPodLogs(context.Context, string, *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error)
}

type PlatformClusterOpsService struct {
	adminv1.UnimplementedPlatformClusterOpsServiceServer
	client ClusterOpsPlatformClient
}

func NewPlatformClusterOpsService(client ClusterOpsPlatformClient) *PlatformClusterOpsService {
	return &PlatformClusterOpsService{client: client}
}

func (s *PlatformClusterOpsService) actor(ctx context.Context, method string) (string, error) {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil || p == nil || p.ID == 0 {
		return "", errors.Unauthorized("INVALID_LOGIN", "login required")
	}
	policy, ok := auth.ClusterOpsPolicy("/admin.service.v1.PlatformClusterOpsService/" + method)
	if !ok || p.Type != auth.SubjectUser || p.TenantID != 0 ||
		p.Claims == nil || p.Claims.GetTenantId() != 0 || p.Claims.GetUserId() != p.ID ||
		!hasPlatformRole(p.Roles) || !hasPermission(p.Claims, policy.Permission) {
		return "", errors.Forbidden("PLATFORM_CLUSTEROPS_DENIED", "platform cluster operations permission required")
	}
	if s.client == nil {
		return "", clusterOpsPublicError("CLUSTEROPS_UNAVAILABLE", nil)
	}
	return p.Actor()
}

func invalidClusterOpsRequest() error {
	return errors.BadRequest("INVALID_CLUSTEROPS_REQUEST", "cluster operations request is incomplete")
}

func validClusterOpsType(value *catalogv1.ClusterOpsResourceTypeRef) bool {
	return value != nil && value.Version != "" && value.Resource != ""
}

func toClusterOpsResourceType(value *catalogv1.ClusterOpsResourceTypeRef) *clusteropsv1.ResourceTypeRef {
	if value == nil {
		return nil
	}
	return &clusteropsv1.ResourceTypeRef{Group: value.Group, Version: value.Version, Resource: value.Resource}
}

func toClusterOpsResourceRef(clusterID string, typ *catalogv1.ClusterOpsResourceTypeRef, namespace, name string) *clusteropsv1.ResourceRef {
	return &clusteropsv1.ResourceRef{
		ClusterId: clusterID, Type: toClusterOpsResourceType(typ), Namespace: namespace, Name: name,
	}
}

func wireClusterOpsResourceType(value *clusteropsv1.ResourceTypeRef) *catalogv1.ClusterOpsResourceTypeRef {
	if value == nil {
		return nil
	}
	return &catalogv1.ClusterOpsResourceTypeRef{Group: value.Group, Version: value.Version, Resource: value.Resource}
}

func wireClusterOpsResourceRef(value *clusteropsv1.ResourceRef) *catalogv1.ClusterOpsResourceRef {
	if value == nil {
		return nil
	}
	return &catalogv1.ClusterOpsResourceRef{
		ClusterId: value.ClusterId, Type: wireClusterOpsResourceType(value.Type), Namespace: value.Namespace, Name: value.Name,
	}
}

func wireClusterOpsResourceDocument(value *clusteropsv1.ResourceDocument) *catalogv1.ClusterOpsResourceDocument {
	if value == nil {
		return nil
	}
	return &catalogv1.ClusterOpsResourceDocument{
		Ref: wireClusterOpsResourceRef(value.Ref), Uid: value.Uid,
		ResourceVersion: value.ResourceVersion, ObjectJson: value.ObjectJson,
		ReadOnly: value.ReadOnly, ReadOnlyReason: value.ReadOnlyReason,
		SecretDataRedacted: value.SecretDataRedacted, SecretKeys: append([]string{}, value.SecretKeys...),
	}
}

func wireClusterOpsResourceDescriptor(value *clusteropsv1.ResourceDescriptor) *catalogv1.ClusterOpsResourceDescriptor {
	if value == nil {
		return nil
	}
	return &catalogv1.ClusterOpsResourceDescriptor{
		Type: wireClusterOpsResourceType(value.Type), Kind: value.Kind, Namespaced: value.Namespaced,
		Actions: append([]string{}, value.Actions...), CustomResource: value.CustomResource, CrdName: value.CrdName,
	}
}

func wireClusterOpsOverview(value *clusteropsv1.ClusterOverview) *catalogv1.ClusterOpsClusterOverview {
	if value == nil {
		return nil
	}
	out := &catalogv1.ClusterOpsClusterOverview{
		ClusterId: value.ClusterId, KubernetesVersion: value.KubernetesVersion,
		NodesTotal: value.NodesTotal, NodesReady: value.NodesReady, NamespacesTotal: value.NamespacesTotal,
		PodsTotal: value.PodsTotal, PodPhaseCounts: make(map[string]int64),
		Capacity: make(map[string]string), Allocatable: make(map[string]string), SampledAt: value.SampledAt,
	}
	for key, count := range value.PodPhaseCounts {
		out.PodPhaseCounts[key] = count
	}
	for key, quantity := range value.Capacity {
		out.Capacity[key] = quantity
	}
	for key, quantity := range value.Allocatable {
		out.Allocatable[key] = quantity
	}
	return out
}

func wireClusterOpsMutation(value *clusteropsv1.MutationResponse) *catalogv1.ClusterOpsMutationResponse {
	if value == nil {
		return nil
	}
	return &catalogv1.ClusterOpsMutationResponse{Resource: wireClusterOpsResourceDocument(value.Resource), DryRun: value.DryRun}
}

func (s *PlatformClusterOpsService) ListClusters(ctx context.Context, req *catalogv1.ClusterOpsListClustersRequest) (*catalogv1.ClusterOpsListClustersResponse, error) {
	actor, err := s.actor(ctx, "ListClusters")
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.ListClusters(ctx, actor, &emptypb.Empty{})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	out := &catalogv1.ClusterOpsListClustersResponse{Items: make([]*catalogv1.ClusterOpsCluster, 0, len(value.Items))}
	for _, item := range value.Items {
		if item == nil {
			return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
		}
		out.Items = append(out.Items, &catalogv1.ClusterOpsCluster{ClusterId: item.ClusterId, DisplayName: item.DisplayName})
	}
	return out, nil
}

func (s *PlatformClusterOpsService) GetClusterOverview(ctx context.Context, req *catalogv1.ClusterOpsGetClusterOverviewRequest) (*catalogv1.ClusterOpsClusterOverview, error) {
	actor, err := s.actor(ctx, "GetClusterOverview")
	if err != nil {
		return nil, err
	}
	if req == nil || req.ClusterId == "" {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.GetClusterOverview(ctx, actor, &clusteropsv1.GetClusterOverviewRequest{ClusterId: req.ClusterId})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	return wireClusterOpsOverview(value), nil
}

func (s *PlatformClusterOpsService) ListResourceTypes(ctx context.Context, req *catalogv1.ClusterOpsListResourceTypesRequest) (*catalogv1.ClusterOpsListResourceTypesResponse, error) {
	actor, err := s.actor(ctx, "ListResourceTypes")
	if err != nil {
		return nil, err
	}
	if req == nil || req.ClusterId == "" {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.ListResourceTypes(ctx, actor, &clusteropsv1.ListResourceTypesRequest{ClusterId: req.ClusterId})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	out := &catalogv1.ClusterOpsListResourceTypesResponse{Items: make([]*catalogv1.ClusterOpsResourceDescriptor, 0, len(value.Items))}
	for _, item := range value.Items {
		if item == nil {
			return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
		}
		out.Items = append(out.Items, wireClusterOpsResourceDescriptor(item))
	}
	return out, nil
}

func (s *PlatformClusterOpsService) ListResources(ctx context.Context, req *catalogv1.ClusterOpsListResourcesRequest) (*catalogv1.ClusterOpsListResourcesResponse, error) {
	actor, err := s.actor(ctx, "ListResources")
	if err != nil {
		return nil, err
	}
	if req == nil || req.ClusterId == "" || req.Version == "" || req.Resource == "" ||
		(req.PageSize != nil && (*req.PageSize == 0 || *req.PageSize > 200)) {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.ListResources(ctx, actor, &clusteropsv1.ListResourcesRequest{
		ClusterId: req.ClusterId, Type: &clusteropsv1.ResourceTypeRef{Group: req.Group, Version: req.Version, Resource: req.Resource},
		Namespace: req.Namespace, AllNamespaces: req.AllNamespaces, PageSize: req.GetPageSize(),
		ContinueToken: req.ContinueToken, Name: req.Name, LabelSelector: req.LabelSelector, FieldSelector: req.FieldSelector,
	})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	out := &catalogv1.ClusterOpsListResourcesResponse{
		Items:             make([]*catalogv1.ClusterOpsResourceDocument, 0, len(value.Items)),
		NextContinueToken: value.NextContinueToken, ListResourceVersion: value.ListResourceVersion,
	}
	for _, item := range value.Items {
		if item == nil {
			return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
		}
		out.Items = append(out.Items, wireClusterOpsResourceDocument(item))
	}
	return out, nil
}

func (s *PlatformClusterOpsService) GetResource(ctx context.Context, req *catalogv1.ClusterOpsGetResourceRequest) (*catalogv1.ClusterOpsGetResourceResponse, error) {
	actor, err := s.actor(ctx, "GetResource")
	if err != nil {
		return nil, err
	}
	if req == nil || req.ClusterId == "" || req.Name == "" || req.Version == "" || req.Resource == "" {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.GetResource(ctx, actor, &clusteropsv1.GetResourceRequest{
		Ref:               toClusterOpsResourceRef(req.ClusterId, &catalogv1.ClusterOpsResourceTypeRef{Group: req.Group, Version: req.Version, Resource: req.Resource}, req.Namespace, req.Name),
		IncludeSecretData: req.IncludeSecretData,
	})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil || value.Resource == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	return &catalogv1.ClusterOpsGetResourceResponse{Resource: wireClusterOpsResourceDocument(value.Resource), ManifestYaml: value.ManifestYaml}, nil
}

func (s *PlatformClusterOpsService) CreateResource(ctx context.Context, req *catalogv1.ClusterOpsCreateResourceRequest) (*catalogv1.ClusterOpsMutationResponse, error) {
	actor, err := s.actor(ctx, "CreateResource")
	if err != nil {
		return nil, err
	}
	if req == nil || req.ClusterId == "" || req.Data == nil || !validClusterOpsType(req.Data.Type) || req.Data.Name == "" || req.Data.ManifestYaml == "" {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.CreateResource(ctx, actor, &clusteropsv1.CreateResourceRequest{
		Ref:          toClusterOpsResourceRef(req.ClusterId, req.Data.Type, req.Data.Namespace, req.Data.Name),
		ManifestYaml: req.Data.ManifestYaml, DryRun: req.Data.DryRun,
	})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil || value.Resource == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	return wireClusterOpsMutation(value), nil
}

func (s *PlatformClusterOpsService) UpdateResource(ctx context.Context, req *catalogv1.ClusterOpsUpdateResourceRequest) (*catalogv1.ClusterOpsMutationResponse, error) {
	actor, err := s.actor(ctx, "UpdateResource")
	if err != nil {
		return nil, err
	}
	if req == nil || req.ClusterId == "" || req.Name == "" || req.Data == nil || !validClusterOpsType(req.Data.Type) ||
		req.Data.ManifestYaml == "" || req.Data.ExpectedUid == "" || req.Data.ExpectedResourceVersion == "" {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.UpdateResource(ctx, actor, &clusteropsv1.UpdateResourceRequest{
		Ref:          toClusterOpsResourceRef(req.ClusterId, req.Data.Type, req.Data.Namespace, req.Name),
		ManifestYaml: req.Data.ManifestYaml, ExpectedUid: req.Data.ExpectedUid,
		ExpectedResourceVersion: req.Data.ExpectedResourceVersion, DryRun: req.Data.DryRun,
	})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil || value.Resource == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	return wireClusterOpsMutation(value), nil
}

func (s *PlatformClusterOpsService) DeleteResource(ctx context.Context, req *catalogv1.ClusterOpsDeleteResourceRequest) (*catalogv1.ClusterOpsDeleteResourceResponse, error) {
	actor, err := s.actor(ctx, "DeleteResource")
	if err != nil {
		return nil, err
	}
	if req == nil || req.ClusterId == "" || req.Name == "" || req.Version == "" || req.Resource == "" || req.ExpectedUid == "" {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.DeleteResource(ctx, actor, &clusteropsv1.DeleteResourceRequest{
		Ref:         toClusterOpsResourceRef(req.ClusterId, &catalogv1.ClusterOpsResourceTypeRef{Group: req.Group, Version: req.Version, Resource: req.Resource}, req.Namespace, req.Name),
		ExpectedUid: req.ExpectedUid,
	})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	return &catalogv1.ClusterOpsDeleteResourceResponse{DeletionRequested: value.DeletionRequested, AlreadyAbsent: value.AlreadyAbsent}, nil
}

func (s *PlatformClusterOpsService) ScaleWorkload(ctx context.Context, req *catalogv1.ClusterOpsScaleWorkloadRequest) (*catalogv1.ClusterOpsScaleWorkloadResponse, error) {
	actor, err := s.actor(ctx, "ScaleWorkload")
	if err != nil {
		return nil, err
	}
	if req == nil || req.ClusterId == "" || req.Name == "" || req.Data == nil || !validClusterOpsType(req.Data.Type) ||
		req.Data.ExpectedUid == "" || req.Data.ExpectedResourceVersion == "" || req.Data.Replicas == nil || *req.Data.Replicas < 0 {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.ScaleWorkload(ctx, actor, &clusteropsv1.ScaleWorkloadRequest{
		Ref:         toClusterOpsResourceRef(req.ClusterId, req.Data.Type, req.Data.Namespace, req.Name),
		ExpectedUid: req.Data.ExpectedUid, ExpectedResourceVersion: req.Data.ExpectedResourceVersion,
		Replicas: req.Data.Replicas, DryRun: req.Data.DryRun,
	})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	return &catalogv1.ClusterOpsScaleWorkloadResponse{
		Ref: wireClusterOpsResourceRef(value.Ref), Uid: value.Uid, ResourceVersion: value.ResourceVersion,
		DesiredReplicas: value.DesiredReplicas, ObservedReplicas: value.ObservedReplicas, DryRun: value.DryRun,
	}, nil
}

func (s *PlatformClusterOpsService) RestartWorkload(ctx context.Context, req *catalogv1.ClusterOpsRestartWorkloadRequest) (*catalogv1.ClusterOpsMutationResponse, error) {
	actor, err := s.actor(ctx, "RestartWorkload")
	if err != nil {
		return nil, err
	}
	if req == nil || req.ClusterId == "" || req.Name == "" || req.Data == nil || !validClusterOpsType(req.Data.Type) ||
		req.Data.ExpectedUid == "" || req.Data.ExpectedResourceVersion == "" {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.RestartWorkload(ctx, actor, &clusteropsv1.RestartWorkloadRequest{
		Ref:         toClusterOpsResourceRef(req.ClusterId, req.Data.Type, req.Data.Namespace, req.Name),
		ExpectedUid: req.Data.ExpectedUid, ExpectedResourceVersion: req.Data.ExpectedResourceVersion, DryRun: req.Data.DryRun,
	})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil || value.Resource == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	return wireClusterOpsMutation(value), nil
}

func (s *PlatformClusterOpsService) OpenPodLogs(ctx context.Context, req *catalogv1.ClusterOpsStreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
	actor, err := s.actor(ctx, "StreamPodLogs")
	if err != nil {
		return nil, err
	}
	if req == nil || req.ClusterId == "" || req.Namespace == "" || req.PodName == "" ||
		(req.TailLines != nil && *req.TailLines < 0) ||
		(req.SinceSeconds != nil && *req.SinceSeconds <= 0) ||
		(req.LimitBytes != nil && *req.LimitBytes <= 0) ||
		(req.SinceSeconds != nil && req.SinceTime != nil) {
		return nil, invalidClusterOpsRequest()
	}
	if req.SinceTime != nil && req.SinceTime.CheckValid() != nil {
		return nil, invalidClusterOpsRequest()
	}
	value, err := s.client.StreamPodLogs(ctx, actor, &clusteropsv1.StreamPodLogsRequest{
		ClusterId: req.ClusterId, Namespace: req.Namespace, PodName: req.PodName, Container: req.Container,
		ExpectedPodUid: req.ExpectedPodUid, Follow: req.Follow, Previous: req.Previous, Timestamps: req.Timestamps,
		TailLines: req.TailLines, SinceSeconds: req.SinceSeconds, SinceTime: req.SinceTime, LimitBytes: req.LimitBytes,
	})
	if err != nil {
		return nil, mapClusterOpsError(err)
	}
	if value == nil {
		return nil, clusterOpsPublicError("CLUSTEROPS_UPSTREAM_ERROR", nil)
	}
	return value, nil
}
