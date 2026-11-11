package service

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	gp "github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/gpuprotocol"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	"go-wind-admin/app/admin/service/internal/commandcontract"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/protobuf/proto"
)

func TestHygonInstanceFreezePreservesBusinessAndOwnerExecution(t *testing.T) {
	for _, fixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "whole", true: "fixed"}[fixed], func(t *testing.T) {
			plan, _, _ := gpuVector(t)
			devices := uint32(2)
			allocation := acc.AllocationType_PHYSICAL
			mode := acc.SupplyMode_WHOLE_EXCLUSIVE
			key := "devices.example/hcu-whole"
			memory := int64(0)
			cu := uint32(0)
			quotaCode := GpuPhysicalQuotaCode
			units := int64(2)
			if fixed {
				devices = 1
				allocation = acc.AllocationType_FIXED_PARTITION
				mode = acc.SupplyMode_SHARED_FIXED
				key = "devices.example/hcu-fixed"
				memory = 16385
				cu = 4
				quotaCode = GpuSharedQuotaCode
				units = 16385
			}
			plan.Request.DevicesPerReplica = devices
			spec := &acc.GpuBusinessSpec{ProfileId: plan.Request.ProfileId, ProfileVersion: plan.Request.ProfileVersion, ProfileName: "managed Hygon", ModelKey: "hygon-test", Mode: mode, AllocationType: allocation, DevicesPerReplica: devices, MemoryMibPerDevice: memory, ComputeUnitsPerDevice: cu, ResourceKey: key, AdapterId: "hygon-hcu-v1", SpecDigest: strings.Repeat("a", 64)}
			execution := &acc.GpuExecution{SchedulerName: "volcano", QueueName: "gpu", NodeSelector: map[string]string{"accelerator.ani.io/supply-group": plan.Profile.Spec.GroupId, "accelerator.ani.io/model-key": spec.ModelKey, "accelerator.ani.io/baseline-id": plan.Profile.BaselineId}, Resources: map[string]string{key: map[bool]string{false: "2", true: "1"}[fixed]}}
			var err error
			execution.ExecutionDigest, err = gp.GpuExecutionDigest(execution)
			require.NoError(t, err)
			plan.Resolved = &acc.ResolvedGpuRequest{Execution: execution, BusinessSpec: spec, QuotaItems: []*acc.GpuQuotaItem{{QuotaCode: quotaCode, Units: units}}}
			business := instanceSettingsBusiness(t)
			business.ComputeType = "gpu"
			business.GpuSelection = &res.GpuPresetReference{ProfileId: spec.ProfileId, ProfileVersion: spec.ProfileVersion, SpecDigest: spec.SpecDigest, DevicesPerInstance: devices, ClusterId: plan.Request.ClusterId, PoolId: plan.Request.PoolId, Mode: mode, ModelKey: spec.ModelKey, MemoryMibPerDevice: memory, AllocationType: allocation, ResourceKey: key, ComputeUnitsPerDevice: cu, AdapterId: spec.AdapterId}
			binding := NewInstanceGpuBinding(&instanceSettingsResolveProbe{template: business}, nil, business.ClusterId)
			prepared, err := binding.PrepareManagedBusiness(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 9, TenantID: 7}, presetTenant, instanceSettingsData(business))
			require.NoError(t, err)
			raw, err := prepared.freeze(presetTenant, "user", "9", "operation", "resource", plan.Resolved)
			require.NoError(t, err)
			command, err := commandcontract.DecodeGpuCanonical(raw)
			require.NoError(t, err)
			require.Equal(t, units, command.QuotaItems[0].Units)
			frozen, err := res.DecodeBusinessPayload(command.BusinessPayload)
			require.NoError(t, err)
			require.True(t, proto.Equal(spec, frozen.GpuBusinessSpec))
			require.True(t, proto.Equal(execution, frozen.GpuExecution))
			require.Nil(t, frozen.GpuSelection)
			require.False(t, bytes.Contains(raw, []byte("gpu_display")))
			require.Empty(t, frozen.GpuExecution.RuntimeClassName)
			require.Empty(t, frozen.GpuExecution.PodAnnotations)
			require.Len(t, frozen.GpuExecution.Resources, 1)
			altered := proto.Clone(plan.Resolved).(*acc.ResolvedGpuRequest)
			altered.BusinessSpec.SpecDigest = strings.Repeat("b", 64)
			_, err = prepared.freeze(presetTenant, "user", "9", "operation", "resource", altered)
			require.Error(t, err, "a valid but different managed reference cannot be frozen")
		})
	}
}
