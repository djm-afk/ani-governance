//go:build quota_pg

package service

import (
	"context"
	"testing"

	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/zhangzhe-ctrl/ani-modeldev-service/contract/cpup01/conformance"
	compute "github.com/zhangzhe-ctrl/ani-resource-service/api/compute/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/app/admin/service/internal/data/ent"
	"go-wind-admin/app/admin/service/internal/data/ent/api"
	"go-wind-admin/app/admin/service/internal/data/ent/modeldevacceptance"
	"go-wind-admin/app/admin/service/internal/data/ent/permission"
	"go-wind-admin/app/admin/service/internal/data/ent/role"
	"go-wind-admin/app/admin/service/internal/data/ent/rolepermission"
	"go-wind-admin/app/admin/service/internal/data/ent/user"
	"go-wind-admin/app/admin/service/internal/data/ent/userrole"
	"go-wind-admin/app/admin/service/tests/testutil"
	appViewer "go-wind-admin/pkg/entgo/viewer"
	"go-wind-admin/pkg/localdeps/go-crud/viewer"
	"go-wind-admin/pkg/middleware/auth"
)

// The actual current authorization, original-key lookup and PG acceptance are
// exercised. Only the external Compute response is controlled; the unconfigured
// ModelDev client would fail if admission called Prepare/Finalize prematurely.
func TestModelDevGPUDisabledBeforeAcceptance(t *testing.T) {
	c := testutil.NewQuotaPGClient(t)
	testutil.ResetQuotaFixture(t, c)
	sys := appViewer.NewSystemViewerContext(context.Background())
	id := "modeldev-gpu-disabled-" + uuid.NewString()
	tenant, err := c.Client().Tenant.Create().SetName(id).SetCode(id).Save(sys)
	require.NoError(t, err)
	operator, err := c.Client().User.Create().SetTenantID(tenant.ID).SetUsername(id).SetStatus(user.StatusNormal).Save(sys)
	require.NoError(t, err)
	r, err := c.Client().Role.Create().SetTenantID(tenant.ID).SetName(id).SetCode("tenant:" + id).SetType(role.TypeTenant).SetStatus(role.StatusOn).SetDataScope(role.DataScopeAll).Save(sys)
	require.NoError(t, err)
	require.NoError(t, c.Client().UserRole.Create().SetTenantID(tenant.ID).SetUserID(operator.ID).SetRoleID(r.ID).SetStatus(userrole.StatusActive).Exec(sys))
	perm, err := c.Client().Permission.Create().SetName(id).SetCode(id).SetStatus(permission.StatusOn).Save(sys)
	require.NoError(t, err)
	route, err := c.Client().Api.Query().Where(api.ModuleEQ("ModelDevService"), api.PathEQ("/api/v1/modeldev/executions"), api.MethodEQ("POST"), api.ScopeEQ(api.ScopeAdmin)).Only(sys)
	if ent.IsNotFound(err) {
		route, err = c.Client().Api.Create().SetModule("ModelDevService").SetPath("/api/v1/modeldev/executions").SetMethod("POST").SetScope(api.ScopeAdmin).SetBusinessModule(api.BusinessModuleModelDev).SetStatus(api.StatusOn).Save(sys)
	}
	require.NoError(t, err)
	require.NoError(t, c.Client().PermissionApi.Create().SetPermissionID(perm.ID).SetAPIID(route.ID).Exec(sys))
	require.NoError(t, c.Client().RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(r.ID).SetPermissionID(perm.ID).SetStatus(rolepermission.StatusOn).SetEffect(rolepermission.EffectAllow).Exec(sys))
	ctx := viewer.WithContext(context.Background(), appViewer.NewUserViewer(uint64(operator.ID), uint64(tenant.ID), 0, "", []viewer.DataScope{{ScopeType: viewer.ScopeTypeAll}}))
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{Type: auth.SubjectUser, ID: operator.ID, TenantID: tenant.ID})
	intent := conformance.IntentV1()
	plan, _, _ := gpuVector(t)
	spec := plan.Resolved.BusinessSpec
	selection := inferenceCPUSelection(t)
	selection.PresetId, selection.ComputeType = intent.ComputePresetID, "gpu"
	selection.Gpu = &compute.GpuSelection{ProfileId: spec.ProfileId, ProfileVersion: spec.ProfileVersion, SpecDigest: spec.SpecDigest, DevicesPerUnit: spec.DevicesPerReplica, ClusterId: plan.Request.ClusterId, PoolId: plan.Request.PoolId, Mode: spec.Mode, ModelKey: spec.ModelKey, MemoryMibPerDevice: spec.MemoryMibPerDevice, CoreLimitPercent: spec.CoreLimitPercent, AllocationType: spec.AllocationType, ResourceKey: spec.ResourceKey, ComputeUnitsPerDevice: spec.ComputeUnitsPerDevice, AdapterId: spec.AdapterId}
	selection.SelectionDigest, err = compute.SelectionDigest(selection)
	require.NoError(t, err)
	peer := &modelDevGPUComputeProbe{t: t, tenant: tenant.ResourceTenantID, selection: selection}
	bctx := testutil.NewBootstrapContext(nil)
	service := NewModelDevService(data.NewModelDevAuthorizationRepo(c), data.NewTenantRepo(bctx, c), data.NewModelDevAcceptanceRepo(c), &data.ModelDevClient{}, peer)
	input := ModelDevCreateInput{Intent: intent, IdempotencyKey: uuid.NewString()}
	for i := 0; i < 2; i++ {
		result, err := service.CreateExecution(ctx, input)
		require.Nil(t, result)
		require.Equal(t, "MODELDEV_GPU_NOT_READY", kratoserrors.FromError(err).Reason)
	}
	require.Equal(t, 2, peer.calls)
	operations, err := c.Client().QuotaOperation.Query().Count(sys)
	require.NoError(t, err)
	require.Zero(t, operations)
	charges, err := c.Client().QuotaCharge.Query().Count(sys)
	require.NoError(t, err)
	require.Zero(t, charges)
	accepted, err := c.Client().ModelDevAcceptance.Query().Where(modeldevacceptance.TenantIDEQ(tenant.ID)).Count(sys)
	require.NoError(t, err)
	require.Zero(t, accepted, "GPU rejection must leave the same key available for a future supported request")
}

type modelDevGPUComputeProbe struct {
	t         *testing.T
	tenant    string
	selection *compute.ComputeSelection
	calls     int
}

func (p *modelDevGPUComputeProbe) ResolveComputePreset(_ context.Context, tenant, actor string, request *compute.ResolveComputePresetRequest) (*compute.ComputeSelection, error) {
	p.calls++
	require.Equal(p.t, p.tenant, tenant)
	require.Equal(p.t, tenant, request.TenantId)
	require.Equal(p.t, compute.Usage_TRAINING, request.Usage)
	require.Equal(p.t, p.selection.PresetId, request.PresetId)
	require.Contains(p.t, actor, "governance:user:")
	return p.selection, nil
}
