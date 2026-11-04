//go:build quota_pg

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/google/uuid"
	runtimev1 "github.com/liangzai006/ani-inference-service/api/inference/v1"
	modelv1 "github.com/liangzai006/ani-model-service/api/model/v1"
	"github.com/stretchr/testify/require"
	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	obs "github.com/zhangzhe-ctrl/ani-observability-service/api/observability/gpu/v1"
	compute "github.com/zhangzhe-ctrl/ani-resource-service/api/compute/v1"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	view "go-wind-admin/api/gen/go/gpu/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/app/admin/service/internal/data/ent"
	"go-wind-admin/app/admin/service/internal/data/ent/api"
	"go-wind-admin/app/admin/service/internal/data/ent/permission"
	"go-wind-admin/app/admin/service/internal/data/ent/planmodule"
	"go-wind-admin/app/admin/service/internal/data/ent/role"
	"go-wind-admin/app/admin/service/internal/data/ent/rolepermission"
	"go-wind-admin/app/admin/service/internal/data/ent/user"
	"go-wind-admin/app/admin/service/internal/data/ent/userrole"
	"go-wind-admin/app/admin/service/tests/testutil"
	appViewer "go-wind-admin/pkg/entgo/viewer"
	crudviewer "go-wind-admin/pkg/localdeps/go-crud/viewer"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Only generated ACC/Obs and business-owner RPC boundaries are substituted.
// Mapping, original ledger reads, resource authorization and DTOs are real Gov.
type gpuMonitoringACCProbe struct {
	acc.AcceleratorAdminServiceClient
	acc.AcceleratorUsageServiceClient
	deviceCalls int
	devices     func(*acc.ListDevicesRequest) (*acc.ListDevicesResponse, error)
}

func (p *gpuMonitoringACCProbe) ListDevices(_ context.Context, r *acc.ListDevicesRequest, _ ...grpc.CallOption) (*acc.ListDevicesResponse, error) {
	p.deviceCalls++
	return p.devices(r)
}

type gpuMonitoringObsProbe struct {
	calls          int
	platform       bool
	tenant, actor  string
	target         *obs.GpuTarget
	start, end     *timestamppb.Timestamp
	err            error
	physicalEvents bool
}

func (p *gpuMonitoringObsProbe) result(platform bool, tenant, actor string, target *obs.GpuTarget, codes []string) (*obs.QueryGpuResponse, error) {
	p.calls++
	p.platform, p.tenant, p.actor, p.target = platform, tenant, actor, proto.Clone(target).(*obs.GpuTarget)
	if p.err != nil {
		return nil, p.err
	}
	r := &obs.QueryGpuResponse{QueryTime: timestamppb.Now(), IndexComplete: false, IncludedResourceKinds: []string{"container"}}
	for _, code := range codes {
		series := &obs.GpuSeries{MetricCode: code, Unit: "bytes", BaseResolutionSeconds: 30, Quality: &obs.GpuQuality{Status: "PARTIAL"}, Points: []*obs.GpuPoint{{Timestamp: timestamppb.Now(), KnownValue: proto.Float64(512), Quality: &obs.GpuQuality{Status: "PARTIAL"}}}}
		if p.physicalEvents {
			series.EventCode, series.Semantics = "31", "exporter-observed-event"
			series.ExporterProcessStarts = []*timestamppb.Timestamp{timestamppb.New(time.Now().Add(-time.Hour))}
		}
		r.Series = append(r.Series, series)
		if p.physicalEvents {
			other := proto.Clone(series).(*obs.GpuSeries)
			other.EventCode = "79"
			r.Series = append(r.Series, other)
		}
	}
	return r, nil
}
func (p *gpuMonitoringObsProbe) QueryGpuLatest(_ context.Context, platform bool, tenant, actor string, r *obs.QueryGpuLatestRequest) (*obs.QueryGpuResponse, error) {
	return p.result(platform, tenant, actor, r.Target, r.MetricCodes)
}
func (p *gpuMonitoringObsProbe) QueryGpuRange(_ context.Context, platform bool, tenant, actor string, r *obs.QueryGpuRangeRequest) (*obs.QueryGpuResponse, error) {
	p.start, p.end = r.Start, r.End
	return p.result(platform, tenant, actor, r.Target, r.MetricCodes)
}

type gpuMonitoringOwnerProbe struct {
	InstanceOwnerClient
	calls             int
	tenant, actor, id string
	err               error
}

func (p *gpuMonitoringOwnerProbe) Get(_ context.Context, tenant, actor, id string) (*res.Instance, error) {
	p.calls++
	p.tenant, p.actor, p.id = tenant, actor, id
	if p.err != nil {
		return nil, p.err
	}
	return &res.Instance{Id: id}, nil
}

type gpuMonitoringFixture struct {
	svc        *GpuMonitoringService
	client     *ent.Client
	ctx        context.Context
	tenant     *ent.Tenant
	operator   *ent.User
	membership *ent.UserRole
	acc        *gpuMonitoringACCProbe
	obs        *gpuMonitoringObsProbe
	owner      *gpuMonitoringOwnerProbe
	ref        *acc.GpuUsageRef
	device     *acc.PhysicalDevice
}

func newGpuMonitoringFixture(t *testing.T) *gpuMonitoringFixture {
	t.Helper()
	c := testutil.NewQuotaPGClient(t)
	testutil.ResetQuotaFixture(t, c)
	sys := appViewer.NewSystemViewerContext(context.Background())
	bctx := testutil.NewBootstrapContext(nil)
	name := "gpu-monitor-read-" + uuid.NewString()
	p, err := c.Client().Plan.Create().SetName(name).Save(sys)
	require.NoError(t, err)
	tenant, err := c.Client().Tenant.Create().SetName(name).SetCode(name).SetPlanID(p.ID).Save(sys)
	require.NoError(t, err)
	for _, module := range []planmodule.Module{planmodule.Module("INSTANCE"), planmodule.Module("INFERENCE")} {
		require.NoError(t, c.Client().PlanModule.Create().SetPlanID(p.ID).SetModule(module).Exec(sys))
	}
	require.NoError(t, c.Client().PlanQuota.Create().SetPlanID(p.ID).SetQuotaCode(GpuSharedQuotaCode).SetQuotaValue(100000).Exec(sys))
	operator, err := c.Client().User.Create().SetTenantID(tenant.ID).SetUsername(name).SetStatus(user.StatusNormal).Save(sys)
	require.NoError(t, err)
	memberRole, err := c.Client().Role.Create().SetTenantID(tenant.ID).SetName(name).SetCode("tenant:" + name).SetType(role.TypeTenant).SetStatus(role.StatusOn).SetDataScope(role.DataScopeAll).Save(sys)
	require.NoError(t, err)
	membership, err := c.Client().UserRole.Create().SetTenantID(tenant.ID).SetUserID(operator.ID).SetRoleID(memberRole.ID).SetStatus(userrole.StatusActive).Save(sys)
	require.NoError(t, err)
	perm, err := c.Client().Permission.Create().SetName(name).SetCode(name).SetStatus(permission.StatusOn).Save(sys)
	require.NoError(t, err)
	for path, module := range map[string]string{data.InstanceGetPath: "INSTANCE", "/api/v1/inference/services/{resource_id}": "INFERENCE"} {
		route, err := c.Client().Api.Query().Where(api.PathEQ(path), api.MethodEQ("GET"), api.ScopeEQ(api.ScopeAdmin)).Only(sys)
		if ent.IsNotFound(err) {
			route, err = c.Client().Api.Create().SetModule(module).SetPath(path).SetMethod("GET").SetScope(api.ScopeAdmin).SetBusinessModule(api.BusinessModule(module)).SetStatus(api.StatusOn).Save(sys)
		}
		require.NoError(t, err)
		require.NoError(t, c.Client().PermissionApi.Create().SetPermissionID(perm.ID).SetAPIID(route.ID).Exec(sys))
	}
	require.NoError(t, c.Client().RolePermission.Create().SetTenantID(tenant.ID).SetRoleID(memberRole.ID).SetPermissionID(perm.ID).SetStatus(rolepermission.StatusOn).SetEffect(rolepermission.EffectAllow).Exec(sys))
	ctx := crudviewer.WithContext(context.Background(), appViewer.NewUserViewer(uint64(operator.ID), uint64(tenant.ID), 0, "", []crudviewer.DataScope{{ScopeType: crudviewer.ScopeTypeAll}}))
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{Type: auth.SubjectUser, ID: operator.ID, TenantID: tenant.ID})
	plan, _, _ := gpuVector(t)
	business := instanceSettingsBusiness(t)
	business.ComputeType, business.GpuExecution, business.GpuDisplay = "gpu", plan.Resolved.Execution, plan.Resolved.Selection
	payload, err := res.CanonicalBusinessPayload(business)
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	canonical, err := gpuCanonicalJSON(GpuCanonical{SchemaVersion: 3, GpuRequest: plan.Request, BusinessPayload: payload, BusinessPayloadDigest: hex.EncodeToString(sum[:]), MeteringVersion: GpuMeteringVersion, QuotaItems: []GpuQuotaItem{{QuotaCode: GpuSharedQuotaCode, Units: 8192}}})
	require.NoError(t, err)
	ledger := data.NewQuotaLedgerRepo(bctx, c)
	resourceID, createID := uuid.NewString(), uuid.NewString()
	_, err = ledger.Occupy(ctx, &data.QuotaOccupyInput{TenantID: tenant.ID, ResourceTenantID: tenant.ResourceTenantID, ResourceID: resourceID, OperationID: createID, OwnerService: InstanceOwner, Action: InstanceCreateAction, ActorType: "user", ActorID: strconv.FormatUint(uint64(operator.ID), 10), IdempotencyKey: uuid.NewString(), RequestHash: "monitoring-original", CanonicalRequest: string(canonical), Items: []data.QuotaOccupyItem{{QuotaCode: GpuSharedQuotaCode, Units: 8192}}})
	require.NoError(t, err)
	ref := &acc.GpuUsageRef{TenantId: tenant.ResourceTenantID, OwnerService: InstanceOwner, ResourceId: resourceID, CreateOperationId: createID}
	device := &acc.PhysicalDevice{DeviceId: uuid.NewString(), ClusterId: plan.Request.ClusterId, Node: &acc.NodeRef{Name: "secret-node", Uid: uuid.NewString()}, VendorDeviceId: "GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", ModelKey: "test-model"}
	accPeer := &gpuMonitoringACCProbe{}
	obsPeer, owner := &gpuMonitoringObsProbe{}, &gpuMonitoringOwnerProbe{}
	tenants := data.NewTenantRepo(bctx, c)
	accelerator := NewAcceleratorService(&data.AcceleratorClient{Admin: accPeer, Usage: accPeer}, tenants, NewGpuBffLedgerBridge(ledger, nil), nil, nil, NewQuotaAdapterRegistry(), nil)
	instanceSvc := NewInstanceService(nil, NewInstanceGpuBinding(owner, data.NewInstanceAuthorizationRepo(c), device.ClusterId), tenants, nil, nil)
	svc := NewGpuMonitoringService(accelerator, obsPeer, instanceSvc, nil, data.NewGpuResourceReadAuthorizationRepo(c))
	return &gpuMonitoringFixture{svc: svc, client: c.Client(), ctx: ctx, tenant: tenant, operator: operator, membership: membership, acc: accPeer, obs: obsPeer, owner: owner, ref: ref, device: device}
}

func TestGpuMonitoringTenantAndContainerUseTrustedIdentity(t *testing.T) {
	f := newGpuMonitoringFixture(t)
	before, err := f.client.QuotaAccount.Query().Only(appViewer.NewSystemViewerContext(context.Background()))
	require.NoError(t, err)
	r, err := f.svc.GetResourceGpuLatest(f.ctx, &view.GpuResourceLatestRequest{ResourceKind: "container", ResourceId: f.ref.ResourceId})
	require.NoError(t, err)
	require.Equal(t, 1, f.owner.calls)
	require.Equal(t, f.tenant.ResourceTenantID, f.owner.tenant)
	require.Equal(t, "governance:user:"+strconv.FormatUint(uint64(f.operator.ID), 10), f.owner.actor)
	require.Equal(t, "RESOURCE", f.obs.target.Kind)
	require.Equal(t, "container", f.obs.target.ResourceKind)
	require.Empty(t, f.obs.target.Scopes, "business monitoring carries identity without physical scopes")
	require.Equal(t, f.tenant.ResourceTenantID, f.obs.tenant)
	require.False(t, f.obs.platform)
	require.Equal(t, f.ref.CreateOperationId, f.obs.target.UsageRef.CreateOperationId)
	require.Equal(t, f.ref.OwnerService, f.obs.target.UsageRef.OwnerService)
	require.Equal(t, f.ref.ResourceId, f.obs.target.UsageRef.ResourceId)
	require.Equal(t, f.ref.TenantId, f.obs.target.UsageRef.TenantId)
	require.Equal(t, "container", r.ResourceKind)
	require.Nil(t, r.Series[0].Points[0].Value, "unknown complete totals must remain absent")
	require.Equal(t, 512.0, r.Series[0].Points[0].GetKnownValue())
	raw, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(r)
	require.NoError(t, err)
	for _, secret := range []string{"secret-", f.device.DeviceId, f.device.Node.Uid, f.ref.CreateOperationId} {
		require.NotContains(t, string(raw), secret)
	}
	f.owner.err = status.Error(codes.NotFound, "deleted owner resource")
	_, err = f.svc.QueryMyGpu(f.ctx, &view.GpuTenantQueryRequest{Data: &view.GpuQueryData{Window: "30d"}})
	require.NoError(t, err)
	require.Equal(t, "TENANT", f.obs.target.Kind)
	require.Empty(t, f.obs.target.Scopes)
	require.Nil(t, f.obs.target.UsageRef)
	require.Equal(t, 1, f.owner.calls, "tenant history does not enumerate only currently readable owner objects")
	require.InDelta(t, 30*24*time.Hour.Seconds(), f.obs.end.AsTime().Sub(f.obs.start.AsTime()).Seconds(), 1)
	require.Zero(t, f.acc.deviceCalls, "business monitoring never enumerates physical devices")
	after, err := f.client.QuotaAccount.Query().Only(appViewer.NewSystemViewerContext(context.Background()))
	require.NoError(t, err)
	require.Equal(t, before.OccupiedUnits, after.OccupiedUnits, "monitoring must not occupy or release quota")
	platform := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7})
	_, err = f.svc.GetTenantGpuLatest(platform, &view.GpuTenantLatestRequest{TenantId: f.tenant.ID})
	require.NoError(t, err)
	require.True(t, f.obs.platform)
	require.Empty(t, f.obs.tenant, "platform transport never invents a tenant context")
	require.Equal(t, f.tenant.ResourceTenantID, f.obs.target.TenantId)
	require.Empty(t, f.obs.target.Scopes)
}

func TestGpuMonitoringInferenceAdapterPreservesOriginalAndReplicas(t *testing.T) {
	f := newGpuMonitoringFixture(t)
	c := testutil.NewQuotaPGClient(t) // same exclusive DB, no fixture reset
	bctx := testutil.NewBootstrapContext(nil)
	principal, err := auth.PrincipalFromContext(f.ctx)
	require.NoError(t, err)
	plan, _, _ := gpuVector(t)
	plan.Request.ContainerName = "kserve-container"
	selection := inferenceCPUSelection(t)
	selection.ComputeType = "gpu"
	selection.Gpu = &compute.GpuSelection{ProfileId: plan.Request.ProfileId, ProfileVersion: plan.Request.ProfileVersion, SpecDigest: plan.Profile.SpecDigest, DevicesPerUnit: 1, ClusterId: plan.Request.ClusterId, PoolId: plan.Request.PoolId, Mode: plan.Resolved.Selection.Mode, ModelKey: plan.Resolved.Selection.ModelKey, MemoryMibPerDevice: 8192, CoreLimitPercent: 25}
	selection.SelectionDigest, err = compute.SelectionDigest(selection)
	require.NoError(t, err)
	intent := inferencePublicIntent()
	model := &inferenceModelProbe{response: &modelv1.GetModelVersionResponse{Model: &modelv1.Model{TenantId: f.tenant.ResourceTenantID}, Version: &modelv1.ModelVersion{Id: intent.ModelVersionId, Status: "ready"}}}
	owner := &inferenceMissingOwner{}
	binding := NewInferenceComputeBinding(owner, data.NewInferenceAuthorizationRepo(c), model, &inferenceComputeProbe{selection: selection})
	prepared, err := binding.PrepareManagedBusiness(f.ctx, principal, f.tenant.ResourceTenantID, intent)
	require.NoError(t, err)
	createID, resourceID := uuid.NewString(), uuid.NewString()
	actorID := strconv.FormatUint(uint64(principal.ID), 10)
	canonical, err := prepared.freeze(f.tenant.ResourceTenantID, "user", actorID, createID, resourceID, plan.Resolved)
	require.NoError(t, err)
	ledger := data.NewQuotaLedgerRepo(bctx, c)
	_, err = ledger.Occupy(f.ctx, &data.QuotaOccupyInput{TenantID: f.tenant.ID, ResourceTenantID: f.tenant.ResourceTenantID, ResourceID: resourceID, OperationID: createID, ActorType: "user", ActorID: actorID, OwnerService: "ani-inference", Action: InferenceCreateAction, IdempotencyKey: uuid.NewString(), RequestHash: "inference-monitor-original", CanonicalRequest: string(canonical), Items: []data.QuotaOccupyItem{{QuotaCode: GpuSharedQuotaCode, Units: 8192}}})
	require.NoError(t, err)
	owner.service = &runtimev1.InferenceService{Id: resourceID, Replicas: 2, Generation: 1}
	f.svc.inference = NewInferenceServiceWithOwner(nil, owner, data.NewTenantRepo(bctx, c))
	r, err := f.svc.GetResourceGpuLatest(f.ctx, &view.GpuResourceLatestRequest{ResourceKind: "inference", ResourceId: resourceID})
	require.NoError(t, err)
	require.Equal(t, "inference", r.ResourceKind)
	require.Equal(t, resourceID, r.ResourceId)
	require.Empty(t, f.obs.target.Scopes, "replicas are selected from labeled Prometheus records")
	require.Equal(t, createID, f.obs.target.UsageRef.CreateOperationId)
	require.Equal(t, resourceID, f.obs.target.UsageRef.ResourceId)
	require.Equal(t, "ani-inference", f.obs.target.UsageRef.OwnerService)
	require.Equal(t, f.tenant.ResourceTenantID, f.obs.target.UsageRef.TenantId)
	require.Equal(t, 1, owner.gets)
	require.Equal(t, f.tenant.ResourceTenantID, owner.getTenant)
	require.Equal(t, "governance:user:"+actorID, owner.getActor)
	require.Zero(t, f.owner.calls, "inference does not route through the container owner")
}

func TestGpuMonitoringResourceAuthorizationAndTenantIsolation(t *testing.T) {
	f := newGpuMonitoringFixture(t)
	for _, r := range []*view.GpuTenantLatestRequest{{TenantId: f.tenant.ID}} {
		_, err := f.svc.GetMyGpuLatest(f.ctx, r)
		require.Equal(t, "GPU_INVALID_QUERY", kerrors.FromError(err).Reason)
	}
	_, err := f.svc.GetResourceGpuLatest(f.ctx, &view.GpuResourceLatestRequest{ResourceKind: "modeldev", ResourceId: f.ref.ResourceId})
	require.Equal(t, "GPU_RESOURCE_KIND_UNSUPPORTED", kerrors.FromError(err).Reason)
	require.Zero(t, f.obs.calls)
	withoutPermissionReader := NewGpuMonitoringService(f.svc.accelerator, f.obs, f.svc.instances, nil)
	_, err = withoutPermissionReader.GetResourceGpuLatest(f.ctx, &view.GpuResourceLatestRequest{ResourceKind: "container", ResourceId: f.ref.ResourceId})
	require.Equal(t, "GPU_RESOURCE_READ_DENIED", kerrors.FromError(err).Reason)
	_, err = f.svc.GetResourceGpuLatest(f.ctx, &view.GpuResourceLatestRequest{ResourceKind: "container", ResourceId: uuid.NewString()})
	require.Equal(t, "QUOTA_NOT_FOUND", kerrors.FromError(err).Reason, "owner-readable resources still require a trusted original GPU ledger")
	unknownTenant := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: f.operator.ID, TenantID: ^uint32(0)})
	_, err = f.svc.GetMyGpuLatest(unknownTenant, &view.GpuTenantLatestRequest{})
	require.Equal(t, "TENANT_MAPPING_UNAVAILABLE", kerrors.FromError(err).Reason)
	require.Zero(t, f.obs.calls)
	f.owner.err = status.Error(codes.NotFound, "owner resource hidden")
	_, err = f.svc.GetResourceGpuLatest(f.ctx, &view.GpuResourceLatestRequest{ResourceKind: "container", ResourceId: f.ref.ResourceId})
	require.Error(t, err)
	require.Zero(t, f.obs.calls, "owner denial precedes Obs")
	f.owner.err = nil
	require.Zero(t, f.obs.calls, "unauthorized queries never reach Obs")
	require.NoError(t, f.client.UserRole.UpdateOneID(f.membership.ID).SetStatus(userrole.StatusDisabled).Exec(appViewer.NewSystemViewerContext(context.Background())))
	ownerCalls := f.owner.calls
	for _, kind := range []string{"container", "inference"} {
		_, err = f.svc.GetResourceGpuLatest(f.ctx, &view.GpuResourceLatestRequest{ResourceKind: kind, ResourceId: f.ref.ResourceId})
		require.Equal(t, "GPU_RESOURCE_READ_DENIED", kerrors.FromError(err).Reason)
	}
	require.Equal(t, ownerCalls, f.owner.calls, "current role denial occurs before owner reads")
}

func TestGpuMonitoringPhysicalAdminAndEventMetadata(t *testing.T) {
	f := newGpuMonitoringFixture(t)
	device := f.device
	f.acc.devices = func(r *acc.ListDevicesRequest) (*acc.ListDevicesResponse, error) {
		require.NotNil(t, r.Context.Actor)
		require.Equal(t, device.ClusterId, r.ClusterId)
		return &acc.ListDevicesResponse{Items: []*acc.PhysicalDevice{device}}, nil
	}
	f.obs.physicalEvents = true
	platform := auth.NewPrincipalContext(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7})
	r, err := f.svc.GetPhysicalGpuLatest(platform, &view.GpuPhysicalLatestRequest{ClusterId: device.ClusterId, DeviceId: device.DeviceId, MetricCodes: []string{"gpu.physical.xid_errors_total"}})
	require.NoError(t, err)
	require.True(t, f.obs.platform)
	require.Empty(t, f.obs.tenant)
	require.Equal(t, device.Node.Uid, f.obs.target.Scopes[0].NodeUid)
	require.Equal(t, device.DeviceId, f.obs.target.Scopes[0].DeviceId)
	require.Len(t, r.Series, 2, "one event metric can return multiple bounded Xid dimensions")
	require.Equal(t, "31", r.Series[0].EventCode)
	require.Equal(t, "79", r.Series[1].EventCode)
	require.Equal(t, "exporter-observed-event", r.Series[0].Semantics)
	require.Len(t, r.Series[0].ExporterProcessStarts, 1)
	_, err = f.svc.GetPhysicalGpuLatest(f.ctx, &view.GpuPhysicalLatestRequest{ClusterId: device.ClusterId, DeviceId: device.DeviceId})
	require.Equal(t, "PLATFORM_REQUIRED", kerrors.FromError(err).Reason)
	for _, principal := range []*auth.Principal{{Type: auth.SubjectAPIKey, ID: 7, TenantID: f.tenant.ID}, {Type: auth.SubjectUser, ID: 7}} {
		_, err = f.svc.GetMyGpuLatest(auth.NewPrincipalContext(context.Background(), principal), &view.GpuTenantLatestRequest{})
		require.Error(t, err)
	}
	f.obs.err = status.Error(codes.ResourceExhausted, "secret upstream body")
	_, err = f.svc.GetPhysicalGpuLatest(platform, &view.GpuPhysicalLatestRequest{ClusterId: device.ClusterId, DeviceId: device.DeviceId})
	require.Equal(t, "GPU_QUERY_BUDGET_EXCEEDED", kerrors.FromError(err).Reason)
	require.NotContains(t, err.Error(), "secret")
}
