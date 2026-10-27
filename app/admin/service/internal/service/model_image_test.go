package service

import (
	"context"
	"strings"
	"testing"

	modelv1 "github.com/liangzai006/ani-model-service/api/model/v1"
	"github.com/stretchr/testify/require"
	imagev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/image/v1"
	catalogv1 "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type modelImageCatalogProbe struct {
	get func(context.Context, string, string, *imagev1.GetImageRequest) (*imagev1.GetImageResponse, error)
}

func (p modelImageCatalogProbe) GetImage(ctx context.Context, tenant, actor string, r *imagev1.GetImageRequest) (*imagev1.GetImageResponse, error) {
	return p.get(ctx, tenant, actor, r)
}

func TestModelCatalogSelectionFreezesReferenceAndRejectsForeignImage(t *testing.T) {
	ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{ID: 7, TenantID: 42, Type: auth.SubjectUser})
	for _, scope := range []string{"tenant", "platform"} {
		t.Run(scope, func(t *testing.T) {
			tenant := "11111111-1111-4111-8111-111111111111"
			id := "img_" + strings.Repeat("a", 32)
			parsed, err := imageScope(scope)
			require.NoError(t, err)
			catalog := new(imageJointPeer).registration(tenant, parsed, id)
			calls := 0
			owner := &modelOwnerVersionProbe{create: func(_ context.Context, gotTenant, actor string, r *modelv1.CreateModelVersionRequest) (*modelv1.ModelVersion, error) {
				calls++
				require.Equal(t, tenant, gotTenant)
				require.Equal(t, "governance:user:7", actor)
				require.Equal(t, id, r.RecommendedImageId)
				require.Equal(t, scope, r.RecommendedImageScope)
				require.Equal(t, catalog.ResolvedReference, r.EngineImage)
				return &modelv1.ModelVersion{Id: "version-1", ModelId: "model-1", Version: "v1", EngineImage: r.EngineImage, RecommendedImageId: r.RecommendedImageId, RecommendedImageScope: r.RecommendedImageScope}, nil
			}}
			images := modelImageCatalogProbe{get: func(_ context.Context, gotTenant, actor string, r *imagev1.GetImageRequest) (*imagev1.GetImageResponse, error) {
				require.Equal(t, tenant, gotTenant)
				require.Equal(t, "governance:user:7", actor)
				require.Equal(t, id, r.ImageId)
				require.Equal(t, parsed, r.Scope)
				return &imagev1.GetImageResponse{Image: catalog}, nil
			}}
			service := NewModelService(owner, modelTenantProbe{}, images)
			input := &catalogv1.CreateModelVersionRequest{ModelId: "model-1", Version: "v1", IdempotencyKey: "create-version", EngineType: "vllm", StartupCommand: "exec vllm serve /models", RecommendedImageId: id, RecommendedImageScope: scope}
			result, err := service.CreateModelVersion(ctx, input)
			require.NoError(t, err)
			require.Equal(t, id, result.Version.RecommendedImageId)
			require.Equal(t, scope, result.Version.RecommendedImageScope)
			require.Equal(t, catalog.ResolvedReference, result.Version.EngineImage)
			require.Empty(t, input.EngineImage, "fixed reference is server-owned; do not mutate the inbound request")
			input.EngineImage = "registry.invalid/other:tag"
			_, err = service.CreateModelVersion(ctx, input)
			require.Error(t, err)
			require.Equal(t, 1, calls)
			input.EngineImage = ""
			catalog.UnregisteredAt = timestamppb.Now()
			_, err = service.CreateModelVersion(ctx, input)
			require.Error(t, err)
			require.Equal(t, 1, calls)
			catalog.UnregisteredAt = nil
			catalog.TenantId = "foreign-tenant"
			_, err = service.CreateModelVersion(ctx, input)
			require.Error(t, err)
			require.Equal(t, 1, calls)
		})
	}
}

func TestModelCatalogBundleFreezesReference(t *testing.T) {
	tenant := "11111111-1111-4111-8111-111111111111"
	id := "img_" + strings.Repeat("a", 32)
	catalog := new(imageJointPeer).registration(tenant, imagev1.ImageScope_IMAGE_SCOPE_PLATFORM, id)
	images := modelImageCatalogProbe{get: func(_ context.Context, gotTenant, actor string, r *imagev1.GetImageRequest) (*imagev1.GetImageResponse, error) {
		require.Equal(t, tenant, gotTenant)
		require.Equal(t, imagev1.ImageScope_IMAGE_SCOPE_PLATFORM, r.Scope)
		return &imagev1.GetImageResponse{Image: catalog}, nil
	}}
	owner := &modelOwnerProbe{bundle: func(_ context.Context, gotTenant, actor string, r *modelv1.ImportModelBundleRequest) (*modelv1.ImportModelBundleResponse, error) {
		require.Equal(t, id, r.Version.RecommendedImageId)
		require.Equal(t, "platform", r.Version.RecommendedImageScope)
		require.Equal(t, catalog.ResolvedReference, r.Version.EngineImage)
		return &modelv1.ImportModelBundleResponse{Model: &modelv1.Model{Id: "model-1", TenantId: tenant}, Version: &modelv1.ModelVersion{Id: "version-1", Version: "v1", RecommendedImageId: id, RecommendedImageScope: "platform", EngineImage: r.Version.EngineImage}, Task: &modelv1.ImportTask{TaskId: "task-1"}}, nil
	}}
	service := NewModelService(owner, modelTenantProbe{}, images)
	ctx := auth.NewPrincipalContext(context.Background(), &auth.Principal{ID: 7, TenantID: 42, Type: auth.SubjectUser})
	reply, err := service.ImportModelBundle(ctx, &catalogv1.ImportModelBundleRequest{IdempotencyKey: "bundle-selected", Model: &catalogv1.ImportModelBundleModelData{Name: "model", Capabilities: []string{"text-generation"}}, Version: &catalogv1.ImportModelBundleVersionData{Version: "v1", Format: "safetensors", EngineType: "vllm", StartupCommand: "exec vllm serve /models", RecommendedImageId: id, RecommendedImageScope: "platform"}, Source: &catalogv1.ImportModelBundleSourceData{Provider: "modelscope", RepoId: "Qwen/Qwen3-0.6B", Revision: "master"}})
	require.NoError(t, err)
	require.Equal(t, catalog.ResolvedReference, reply.Version.EngineImage)
	require.Equal(t, id, reply.Version.RecommendedImageId)
}
