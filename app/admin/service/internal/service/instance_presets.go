package service

import (
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/google/uuid"
	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	view "go-wind-admin/api/gen/go/instance/service/v1"
	"go-wind-admin/app/admin/service/internal/data"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type InstancePresetOwner interface {
	AdminListPresets(context.Context, string, *res.AdminListPresetsRequest) (*res.ListPresetsResponse, error)
	AdminGetPreset(context.Context, string, *res.AdminGetPresetRequest) (*res.Preset, error)
	AdminCreatePreset(context.Context, string, *res.AdminCreatePresetRequest) (*res.Preset, error)
	AdminUpdatePreset(context.Context, string, *res.AdminUpdatePresetRequest) (*res.Preset, error)
	AdminSetPresetEnabled(context.Context, string, *res.AdminSetPresetEnabledRequest) (*res.Preset, error)
	AdminDeletePreset(context.Context, string, *res.AdminDeletePresetRequest) (*emptypb.Empty, error)
}
type InstanceGPUAdmin interface {
	AdminListProfiles(context.Context, *acc.AdminListProfilesRequest, ...grpc.CallOption) (*acc.ListProfilesResponse, error)
	ListPools(context.Context, *acc.ListPoolsRequest, ...grpc.CallOption) (*acc.ListPoolsResponse, error)
	ListSupplyGroups(context.Context, *acc.ListSupplyGroupsRequest, ...grpc.CallOption) (*acc.ListSupplyGroupsResponse, error)
}
type InstanceGPUCatalog interface {
	ListProfiles(context.Context, *acc.ListProfilesRequest, ...grpc.CallOption) (*acc.ListProfilesResponse, error)
}
type InstanceCatalogConfig struct {
	ClusterID string
	Admin     InstanceGPUAdmin
	Catalog   InstanceGPUCatalog
	Cluster   InstanceSchedulingCluster
	Settings  *InstanceSchedulingPolicy
}

// InstanceSchedulingCluster accesses the one deployment-managed cluster.
type InstanceSchedulingCluster interface {
	ReadSchedulerConfig(context.Context) (string, string, error)
	WriteSchedulerConfig(context.Context, string, string) (string, string, error)
	PriorityClassExists(context.Context, string) error
}

func instanceBusinessError(err error) error {
	reason := "INVALID_INSTANCE_REQUEST"
	if stderrors.Is(err, res.ErrInvalidScheduling) {
		reason = "INVALID_SCHEDULING_OPTIONS"
	}
	if stderrors.Is(err, res.ErrSchedulingConflict) {
		reason = "SCHEDULING_FIELD_CONFLICT"
	}
	return errors.BadRequest(reason, "invalid frozen instance business")
}

func instanceCatalogUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.Nil && parsed.String() == value
}

func (s *InstanceService) allowedPresetGPU(ctx context.Context, tenant string) ([]*res.GpuPresetReference, error) {
	p, err := gpuPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	gate, ok := s.binding.authorization.(interface {
		AuthorizeCatalog(context.Context, uint32, uint32) error
	})
	if !ok {
		return nil, data.ErrInferenceAuthorizationDenied
	}
	if err = gate.AuthorizeCatalog(ctx, p.TenantID, p.ID); err != nil {
		return nil, err
	}
	if s.catalog.Catalog == nil || s.catalog.ClusterID == "" {
		return nil, acceleratorUnavailable()
	}
	delegation := &acc.TenantContext{RequestId: uuid.NewString(), TenantId: tenant, Actor: &acc.Actor{Type: "user", Id: strconv.FormatUint(uint64(p.ID), 10)}}
	var allowed []*res.GpuPresetReference
	token := ""
	tokens := map[string]bool{}
	profiles := map[string]bool{}
	for {
		reply, err := s.catalog.Catalog.ListProfiles(ctx, &acc.ListProfilesRequest{Context: delegation, ClusterId: s.catalog.ClusterID, Page: &acc.Page{Size: 200, Token: token}})
		if err != nil {
			return nil, err
		}
		if reply == nil || len(reply.Scopes) != len(reply.Items) {
			return nil, acceleratorInvalidResponse()
		}
		scopes := map[string]*acc.ProfileScope{}
		for _, scope := range reply.Scopes {
			if scope == nil || scope.ProfileId == "" || scope.ProfileVersion == 0 || scope.SpecDigest == "" || scope.ClusterId != s.catalog.ClusterID {
				return nil, acceleratorInvalidResponse()
			}
			if !instanceCatalogUUID(scope.ProfileId) || !instanceCatalogUUID(scope.PoolId) {
				return nil, acceleratorInvalidResponse()
			}
			key := fmt.Sprintf("%s/%d", scope.ProfileId, scope.ProfileVersion)
			if scopes[key] != nil {
				return nil, acceleratorInvalidResponse()
			}
			scopes[key] = scope
		}
		for _, profile := range reply.Items {
			if profile == nil || !profile.Published || profile.Spec == nil {
				return nil, acceleratorInvalidResponse()
			}
			digest, err := gpuSpecDigest(profile)
			if err != nil || digest != profile.SpecDigest {
				return nil, acceleratorInvalidResponse()
			}
			key := fmt.Sprintf("%s/%d", profile.ProfileId, profile.ProfileVersion)
			scope := scopes[key]
			if profiles[key] || scope == nil || scope.SpecDigest != profile.SpecDigest {
				return nil, acceleratorInvalidResponse()
			}
			profiles[key] = true
			allowed = append(allowed, &res.GpuPresetReference{ProfileId: scope.ProfileId, ProfileVersion: scope.ProfileVersion, SpecDigest: scope.SpecDigest, ClusterId: scope.ClusterId, PoolId: scope.PoolId})
		}
		// Leave headroom under the existing gRPC message limit. A larger catalog is
		// unavailable, never silently truncated into an authorization decision.
		if proto.Size(&res.ListPresetsRequest{AllowedGpuProfiles: allowed}) > 3*1024*1024 {
			return nil, acceleratorUnavailable()
		}
		token = reply.NextToken
		if token == "" {
			return allowed, nil
		}
		if tokens[token] {
			return nil, acceleratorInvalidResponse()
		}
		tokens[token] = true
	}
}

func resolveInstancePage(p, s *uint32) (uint32, uint32, error) {
	page, size := uint32(1), uint32(20)
	if p != nil {
		page = *p
	}
	if s != nil {
		size = *s
	}
	if page == 0 || size == 0 || size > 100 {
		return 0, 0, errors.BadRequest("INVALID_INSTANCE_REQUEST", "invalid page or pageSize")
	}
	return page, size, nil
}
func wireInstancePreset(p *res.Preset) *view.Preset {
	if p == nil {
		return nil
	}
	out := &view.Preset{Id: p.Id, Code: p.Code, Name: p.Name, Description: p.Description, ComputeType: p.ComputeType, CpuMillis: p.CpuMillis, MemoryMib: p.MemoryMib, Enabled: p.Enabled}
	if p.CreatedAt != nil {
		out.CreatedAt = p.CreatedAt.AsTime().UTC().Format(time.RFC3339Nano)
	}
	if p.UpdatedAt != nil {
		out.UpdatedAt = p.UpdatedAt.AsTime().UTC().Format(time.RFC3339Nano)
	}
	if g := p.Gpu; g != nil {
		out.Gpu = &view.GpuSummary{ProfileId: g.ProfileId, ProfileVersion: g.ProfileVersion, Mode: g.Mode.String(), ModelKey: g.ModelKey, DevicesPerInstance: g.DevicesPerInstance, CoreLimitPercent: g.CoreLimitPercent}
		if g.SharedMemoryMibPerDevice != nil {
			out.Gpu.SharedMemoryMibPerDevice = wrapperspb.Int64(*g.SharedMemoryMibPerDevice)
		}
		if g.SharedMemoryMibPerDevice != nil && *g.SharedMemoryMibPerDevice > 0 && g.DevicesPerInstance > 0 && *g.SharedMemoryMibPerDevice <= math.MaxInt64/int64(g.DevicesPerInstance) {
			n := *g.SharedMemoryMibPerDevice * int64(g.DevicesPerInstance)
			out.Gpu.SharedMemoryMibTotal = wrapperspb.Int64(n)
		}
	}
	return out
}
func (s *InstanceService) presetAdmin(ctx context.Context, permission string) (InstancePresetOwner, string, error) {
	p, err := instancePlatformPrincipal(ctx, permission)
	if err != nil {
		return nil, "", err
	}
	if s.binding == nil {
		return nil, "", errors.ServiceUnavailable("INSTANCE_ADAPTER_UNAVAILABLE", "instance owner unavailable")
	}
	owner, ok := s.binding.client.(InstancePresetOwner)
	if !ok {
		return nil, "", errors.ServiceUnavailable("INSTANCE_ADAPTER_UNAVAILABLE", "instance owner unavailable")
	}
	return owner, "governance:user:" + strconv.FormatUint(uint64(p.ID), 10), nil
}
func (s *InstanceService) AdminListPresets(ctx context.Context, r *view.AdminListPresetsRequest) (*view.AdminListPresetsResponse, error) {
	owner, actor, err := s.presetAdmin(ctx, "instance:preset:admin:list")
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "request required")
	}
	page, size, err := resolveInstancePage(r.Page, r.PageSize)
	if err != nil {
		return nil, err
	}
	reply, err := owner.AdminListPresets(ctx, actor, &res.AdminListPresetsRequest{Page: page, PageSize: size, Name: r.Name, Code: r.Code, ComputeType: r.ComputeType, GpuMode: r.GpuMode, Enabled: r.Enabled})
	if err != nil {
		return nil, err
	}
	if reply == nil {
		return nil, errors.ServiceUnavailable("INSTANCE_INVALID_RESPONSE", "invalid owner response")
	}
	out := &view.AdminListPresetsResponse{Items: []*view.Preset{}, Total: reply.Total}
	for _, p := range reply.Items {
		out.Items = append(out.Items, wireInstancePreset(p))
	}
	return out, nil
}
func (s *InstanceService) AdminGetPreset(ctx context.Context, r *view.PresetRequest) (*view.Preset, error) {
	owner, actor, err := s.presetAdmin(ctx, "instance:preset:admin:read")
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "request required")
	}
	reply, err := owner.AdminGetPreset(ctx, actor, &res.AdminGetPresetRequest{Id: r.Id})
	if err != nil {
		return nil, err
	}
	return wireInstancePreset(reply), nil
}
func (s *InstanceService) presetData(ctx context.Context, actor string, in *view.PresetData, replaceGPU bool) (*res.PresetData, error) {
	if in == nil {
		return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "preset data required")
	}
	out := &res.PresetData{Code: in.Code, Name: in.Name, Description: in.Description, ComputeType: in.ComputeType, CpuMillis: in.CpuMillis, MemoryMib: in.MemoryMib, Enabled: in.Enabled}
	if replaceGPU && in.Gpu != nil {
		if in.ComputeType == "cpu" {
			return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "CPU preset cannot carry GPU")
		}
		g, err := s.resolvePresetGPU(ctx, actor, in.Gpu)
		if err != nil {
			return nil, err
		}
		out.Gpu = g
	}
	return out, nil
}
func (s *InstanceService) AdminCreatePreset(ctx context.Context, r *view.CreatePresetRequest) (*view.Preset, error) {
	owner, actor, err := s.presetAdmin(ctx, "instance:preset:admin:create")
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "request required")
	}
	d, err := s.presetData(ctx, actor, r.Data, true)
	if err != nil {
		return nil, err
	}
	reply, err := owner.AdminCreatePreset(ctx, actor, &res.AdminCreatePresetRequest{Data: d})
	if err != nil {
		return nil, err
	}
	return wireInstancePreset(reply), nil
}
func (s *InstanceService) AdminUpdatePreset(ctx context.Context, r *view.UpdatePresetRequest) (*view.Preset, error) {
	owner, actor, err := s.presetAdmin(ctx, "instance:preset:admin:update")
	if err != nil {
		return nil, err
	}
	if r == nil || r.Data == nil || r.UpdateMask == nil || len(r.UpdateMask.Paths) == 0 {
		return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "data and update_mask required")
	}
	if r.Data.Code != "" {
		return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "code is immutable")
	}
	seen := map[string]bool{}
	gpu := false
	for _, p := range r.UpdateMask.Paths {
		if seen[p] {
			return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "duplicate update path")
		}
		seen[p] = true
		switch p {
		case "name", "description", "compute_type", "cpu_millis", "memory_mib", "enabled":
		case "gpu":
			gpu = true
		default:
			return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "invalid update path")
		}
	}
	if seen["enabled"] && r.Data.Enabled == nil {
		return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "enabled must be present")
	}
	d, err := s.presetData(ctx, actor, r.Data, gpu)
	if err != nil {
		return nil, err
	}
	reply, err := owner.AdminUpdatePreset(ctx, actor, &res.AdminUpdatePresetRequest{Id: r.Id, Data: d, UpdateMask: r.UpdateMask})
	if err != nil {
		return nil, err
	}
	return wireInstancePreset(reply), nil
}
func (s *InstanceService) AdminSetPresetEnabled(ctx context.Context, r *view.SetPresetEnabledRequest) (*view.Preset, error) {
	owner, actor, err := s.presetAdmin(ctx, "instance:preset:admin:update")
	if err != nil {
		return nil, err
	}
	if r == nil || r.Data == nil || r.Data.Enabled == nil {
		return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "enabled must be present")
	}
	reply, err := owner.AdminSetPresetEnabled(ctx, actor, &res.AdminSetPresetEnabledRequest{Id: r.Id, Enabled: r.Data.Enabled})
	if err != nil {
		return nil, err
	}
	return wireInstancePreset(reply), nil
}
func (s *InstanceService) AdminDeletePreset(ctx context.Context, r *view.PresetRequest) (*view.Empty, error) {
	owner, actor, err := s.presetAdmin(ctx, "instance:preset:admin:delete")
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.BadRequest("INVALID_INSTANCE_REQUEST", "request required")
	}
	_, err = owner.AdminDeletePreset(ctx, actor, &res.AdminDeletePresetRequest{Id: r.Id})
	if err != nil {
		return nil, err
	}
	return &view.Empty{}, nil
}

// Admin catalog reads establish managed facts; saving a preset never publishes
// an ACC Profile. The cluster comes from deployment configuration.
func (s *InstanceService) resolvePresetGPU(ctx context.Context, actor string, in *view.PresetGpuInput) (*res.GpuPresetReference, error) {
	invalid := errors.BadRequest("INVALID_GPU_PRESET_REFERENCE", "invalid GPU preset reference")
	if in == nil || in.ProfileVersion < 1 || in.DevicesPerInstance < 1 {
		return nil, invalid
	}
	if !instanceCatalogUUID(in.ProfileId) {
		return nil, invalid
	}
	c := s.catalog
	if c.Admin == nil || c.ClusterID == "" {
		return nil, acceleratorUnavailable()
	}
	admin := &acc.AdminRead{RequestId: uuid.NewString(), Actor: &acc.Actor{Type: "user", Id: actor[len("governance:user:"):]}}
	var profile *acc.GpuProfile
	token := ""
	seen := map[string]bool{}
	for {
		reply, err := c.Admin.AdminListProfiles(ctx, &acc.AdminListProfilesRequest{Context: admin, ClusterId: c.ClusterID, Page: &acc.Page{Size: 200, Token: token}})
		if err != nil {
			return nil, mapAcceleratorError(err)
		}
		if reply == nil {
			return nil, acceleratorInvalidResponse()
		}
		for _, p := range reply.Items {
			if p != nil && p.ProfileId == in.ProfileId && p.ProfileVersion == uint64(in.ProfileVersion) {
				if profile != nil {
					return nil, acceleratorInvalidResponse()
				}
				profile = p
			}
		}
		token = reply.NextToken
		if token == "" {
			break
		}
		if seen[token] {
			return nil, acceleratorInvalidResponse()
		}
		seen[token] = true
	}
	if profile == nil || profile.Spec == nil || !profile.Published || profile.SpecDigest == "" || profile.Spec.MaxDevicesPerReplica < in.DevicesPerInstance {
		return nil, invalid
	}
	digest, err := gpuSpecDigest(profile)
	if err != nil || digest != profile.SpecDigest {
		return nil, invalid
	}
	spec := profile.Spec
	if spec.Mode != acc.SupplyMode_WHOLE_EXCLUSIVE && spec.Mode != acc.SupplyMode_SHARED_FIXED || spec.Mode == acc.SupplyMode_SHARED_FIXED && (in.DevicesPerInstance != 1 || spec.MaxDevicesPerReplica != 1) {
		return nil, invalid
	}
	if spec.Mode == acc.SupplyMode_WHOLE_EXCLUSIVE && (spec.SharedMemoryMib != 0 || spec.CoreLimitPercent != 100) || spec.Mode == acc.SupplyMode_SHARED_FIXED && (spec.SharedMemoryMib <= 0 || spec.CoreLimitPercent < 1 || spec.CoreLimitPercent > 99) {
		return nil, invalid
	}
	pools, err := s.instancePools(ctx, admin)
	if err != nil {
		return nil, err
	}
	var group *acc.SupplyGroup
	for _, pool := range pools {
		token = ""
		seen = map[string]bool{}
		for {
			reply, err := c.Admin.ListSupplyGroups(ctx, &acc.ListSupplyGroupsRequest{Context: admin, PoolId: pool.PoolId, Page: &acc.Page{Size: 200, Token: token}})
			if err != nil {
				return nil, mapAcceleratorError(err)
			}
			if reply == nil {
				return nil, acceleratorInvalidResponse()
			}
			for _, g := range reply.Items {
				if g != nil && g.GroupId == spec.GroupId {
					if group != nil || g.ClusterId != c.ClusterID || g.PoolId != pool.PoolId || g.Baseline == nil || g.Baseline.BaselineId != profile.BaselineId || g.Mode != spec.Mode || g.ModelKey != spec.ModelKey {
						return nil, invalid
					}
					group = g
				}
			}
			token = reply.NextToken
			if token == "" {
				break
			}
			if seen[token] {
				return nil, acceleratorInvalidResponse()
			}
			seen[token] = true
		}
	}
	if group == nil {
		return nil, invalid
	}
	out := &res.GpuPresetReference{ProfileId: profile.ProfileId, ProfileVersion: in.ProfileVersion, SpecDigest: profile.SpecDigest, DevicesPerInstance: in.DevicesPerInstance, ClusterId: group.ClusterId, PoolId: group.PoolId, Mode: spec.Mode, ModelKey: spec.ModelKey, CoreLimitPercent: spec.CoreLimitPercent}
	if spec.Mode == acc.SupplyMode_SHARED_FIXED {
		m := spec.SharedMemoryMib
		out.SharedMemoryMibPerDevice = &m
	}
	return out, nil
}
func (s *InstanceService) instancePools(ctx context.Context, admin *acc.AdminRead) ([]*acc.Pool, error) {
	var items []*acc.Pool
	token := ""
	seen := map[string]bool{}
	for {
		reply, err := s.catalog.Admin.ListPools(ctx, &acc.ListPoolsRequest{Context: admin, ClusterId: s.catalog.ClusterID, Page: &acc.Page{Size: 200, Token: token}})
		if err != nil {
			return nil, mapAcceleratorError(err)
		}
		if reply == nil {
			return nil, acceleratorInvalidResponse()
		}
		for _, p := range reply.Items {
			if p == nil || p.ClusterId != s.catalog.ClusterID {
				return nil, acceleratorInvalidResponse()
			}
			items = append(items, p)
		}
		token = reply.NextToken
		if token == "" {
			return items, nil
		}
		if seen[token] {
			return nil, acceleratorInvalidResponse()
		}
		seen[token] = true
	}
}
