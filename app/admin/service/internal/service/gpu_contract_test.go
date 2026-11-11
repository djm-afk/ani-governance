package service

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/stretchr/testify/require"
	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	protocol "github.com/zhangzhe-ctrl/ani-accelerator-service/pkg/gpuprotocol"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"math"
	"strconv"
	"strings"
	"testing"
)

type gpuPlanFixture struct {
	Request  *acc.GpuRequest
	Profile  *acc.GpuProfile
	Resolved *acc.ResolvedGpuRequest
}

func gpuVector(t *testing.T) (*gpuPlanFixture, *GpuUsageRef, *acc.ProviderBaseline) {
	t.Helper()
	request := &acc.GpuRequest{ClusterId: "10000000-0000-4000-8000-000000000001", PoolId: "10000000-0000-4000-8000-000000000002", ProfileId: "10000000-0000-4000-8000-000000000003", ProfileVersion: 1, Replicas: 1, DevicesPerReplica: 1, ContainerName: "workspace"}
	baseline := &acc.ProviderBaseline{BaselineId: "10000000-0000-4000-8000-000000000004", AdapterId: "volcano-hami-mib-v1", MemoryBlockMib: 1, SplitCount: 10, RuntimeClassName: "nvidia"}
	profile := &acc.GpuProfile{ProfileId: request.ProfileId, ProfileVersion: 1, DisplayName: "T4 shared 8GiB", BaselineId: baseline.BaselineId, State: acc.ProfileState_PROFILE_ACTIVE, Revision: 1, Spec: &acc.ProfileSpec{GroupId: "10000000-0000-4000-8000-000000000005", Mode: acc.SupplyMode_SHARED_FIXED, ModelKey: "nvidia-tesla-t4", MemoryMibPerDevice: 8192, CoreLimitPercent: 25, MaxDevicesPerReplica: 1, IsolationClass: "SOFTWARE_COOPERATIVE", AllocationType: acc.AllocationType_DYNAMIC_SHARED, ResourceKey: "volcano.sh/vgpu-number"}}
	execution := &acc.GpuExecution{SchedulerName: "volcano", QueueName: "gpu", RuntimeClassName: "nvidia", NodeSelector: map[string]string{"accelerator.ani.io/supply-group": profile.Spec.GroupId, "accelerator.ani.io/model-key": profile.Spec.ModelKey, "accelerator.ani.io/baseline-id": profile.BaselineId}, PodAnnotations: map[string]string{"volcano.sh/vgpu-mode": "hami-core"}, Resources: map[string]string{"volcano.sh/vgpu-number": "1", "volcano.sh/vgpu-memory": "8192", "volcano.sh/vgpu-cores": "25"}}
	fixture := &gpuPlanFixture{Request: request, Profile: profile, Resolved: &acc.ResolvedGpuRequest{Execution: execution, QuotaItems: []*acc.GpuQuotaItem{{QuotaCode: GpuSharedQuotaCode, Units: 8192}}}}
	refreshGPUFixture(t, fixture)
	return fixture, &GpuUsageRef{TenantId: "10000000-0000-4000-8000-000000000006", OwnerService: InstanceOwner, ResourceId: "10000000-0000-4000-8000-000000000007", CreateOperationId: "10000000-0000-4000-8000-000000000008"}, baseline
}
func refreshGPUFixture(t *testing.T, p *gpuPlanFixture) {
	t.Helper()
	var err error
	p.Profile.SpecDigest, err = gpuSpecDigest(p.Profile)
	require.NoError(t, err)
	p.Resolved.Execution.ExecutionDigest, err = protocol.GpuExecutionDigest(p.Resolved.Execution)
	require.NoError(t, err)
	g := p.Profile.Spec
	p.Resolved.BusinessSpec = &acc.GpuBusinessSpec{ProfileId: p.Profile.ProfileId, ProfileVersion: p.Profile.ProfileVersion, ProfileName: p.Profile.DisplayName, ModelKey: g.ModelKey, Mode: g.Mode, AllocationType: g.AllocationType, DevicesPerReplica: p.Request.DevicesPerReplica, MemoryMibPerDevice: g.MemoryMibPerDevice, CoreLimitPercent: g.CoreLimitPercent, ComputeUnitsPerDevice: g.ComputeUnitsPerDevice, ResourceKey: g.ResourceKey, AdapterId: "volcano-hami-mib-v1", SpecDigest: p.Profile.SpecDigest}
}
func TestGpuFrozenCanonicalVectors(t *testing.T) {
	p, _, _ := gpuVector(t)
	q, e := GpuResolvedQuota(p.Resolved, p.Request)
	require.NoError(t, e)
	require.EqualValues(t, 8192, q.Units)
}
func TestGpuCanonicalRejectsSemanticMutations(t *testing.T) {
	p, _, _ := gpuVector(t)
	for name, mutate := range map[string]func(*acc.ResolvedGpuRequest){"percentage": func(p *acc.ResolvedGpuRequest) { p.Execution.Resources["volcano.sh/vgpu-memory-percentage"] = "100" }, "quota": func(p *acc.ResolvedGpuRequest) { p.QuotaItems[0].Units++ }, "scheduler": func(p *acc.ResolvedGpuRequest) { p.Execution.SchedulerName = "default-scheduler" }, "zero": func(p *acc.ResolvedGpuRequest) { p.Execution.Resources["volcano.sh/vgpu-number"] = "0" }, "runtime": func(p *acc.ResolvedGpuRequest) { p.Execution.RuntimeClassName = "" }} {
		t.Run(name, func(t *testing.T) {
			changed := proto.Clone(p.Resolved).(*acc.ResolvedGpuRequest)
			mutate(changed)
			changed.Execution.ExecutionDigest, _ = protocol.GpuExecutionDigest(changed.Execution)
			if _, e := GpuResolvedQuota(changed, p.Request); e == nil {
				t.Fatal("invalid execution accepted")
			}
		})
	}
}
func TestGpuCanonicalUnicodeNullInteger(t *testing.T) {
	v := map[string]any{"z": "9223372036854775807", "a": "<&中文>", "b": true}
	b, e := gpuCanonicalJSON(v)
	if e != nil || string(b) != `{"a":"<&中文>","b":true,"z":"9223372036854775807"}` {
		t.Fatal(string(b), e)
	}
	p := &acc.ResolvedGpuRequest{}
	o, e := gpuCanonicalMessage(p.ProtoReflect())
	if e != nil {
		t.Fatal(e)
	}
	b, _ = gpuCanonicalJSON(o)
	if !strings.Contains(string(b), `"business_spec":null`) {
		t.Fatal(string(b))
	}
	r := &acc.GpuRequest{ProfileVersion: ^uint64(0)}
	if _, e = gpuCanonicalMessage(r.ProtoReflect()); e == nil {
		t.Fatal("overflow")
	}
}
func TestGpuCanonicalRejectsFloatMessages(t *testing.T) {
	for _, m := range []proto.Message{wrapperspb.Float(0), wrapperspb.Double(math.NaN()), wrapperspb.Double(math.Inf(1))} {
		if _, e := gpuCanonicalMessage(m.ProtoReflect()); e == nil {
			t.Fatal("float accepted")
		}
	}
}
func TestGpuQuantityBuilderVectorsAndOriginalCharges(t *testing.T) {
	p, _, _ := gpuVector(t)
	for _, devices := range []uint32{1, 2, 4} {
		r := proto.Clone(p.Request).(*acc.GpuRequest)
		r.DevicesPerReplica = devices
		resolved := proto.Clone(p.Resolved).(*acc.ResolvedGpuRequest)
		resolved.BusinessSpec.Mode = acc.SupplyMode_WHOLE_EXCLUSIVE
		resolved.BusinessSpec.AllocationType = acc.AllocationType_PHYSICAL
		resolved.BusinessSpec.DevicesPerReplica = devices
		resolved.BusinessSpec.MemoryMibPerDevice = 15360
		resolved.BusinessSpec.CoreLimitPercent = 100
		resolved.Execution.Resources["volcano.sh/vgpu-number"] = strconv.FormatUint(uint64(devices), 10)
		resolved.Execution.Resources["volcano.sh/vgpu-memory"] = "15360"
		resolved.Execution.Resources["volcano.sh/vgpu-cores"] = "100"
		resolved.Execution.ExecutionDigest, _ = protocol.GpuExecutionDigest(resolved.Execution)
		resolved.QuotaItems[0] = &acc.GpuQuotaItem{QuotaCode: GpuPhysicalQuotaCode, Units: int64(devices)}
		q, e := GpuResolvedQuota(resolved, r)
		if e != nil || q.Units != int64(devices) {
			t.Fatal(q, e)
		}
	}
}
func TestQuotaDurableAckFields(t *testing.T) {
	cmd := &QuotaDispatchCommand{OperationID: "op", ResourceID: "resource"}
	for _, raw := range []string{`{}`, `{"operation_id":"other","resource_id":"resource","accepted":true}`, `{"operation_id":"op","resource_id":"other","accepted":true}`, `{"operation_id":"op","resource_id":"resource","accepted":false}`, `{"operation_id":"other","operation_id":"op","resource_id":"resource","accepted":true}`, `{"operation_id":"op","resource_id":"resource","accepted":true,"Accepted":false}`, `{"operation_id":"op","resource_id":"resource","accepted":true} {}`, `null`, `bad`} {
		if ValidateDurableOwnerAck(cmd, []byte(raw)) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if e := ValidateDurableOwnerAck(cmd, []byte(`{"operation_id":"op","resource_id":"resource","accepted":true}`)); e != nil {
		t.Fatal(e)
	}
}

func TestGpuChargesCompleteAndOrderIndependent(t *testing.T) {
	c := &GpuCanonical{QuotaItems: []GpuQuotaItem{{QuotaCode: GpuSharedQuotaCode, Units: 8192}, {QuotaCode: "storage.bytes", Units: 10}}}
	valid := []QuotaChargeRef{{ChargeID: "storage", QuotaCode: "storage.bytes", ChargedUnits: 10}, {ChargeID: "gpu", QuotaCode: GpuSharedQuotaCode, ChargedUnits: 8192}}
	if e := ValidateGpuCharges(c, valid); e != nil {
		t.Fatal(e)
	}
	for _, invalid := range [][]QuotaChargeRef{nil, valid[:1], {valid[0], valid[0]}, {valid[1], {ChargeID: "gpu", QuotaCode: "storage.bytes", ChargedUnits: 10}}, {valid[0], {ChargeID: "gpu", QuotaCode: GpuSharedQuotaCode, ChargedUnits: 12}}} {
		if e := ValidateGpuCharges(c, invalid); e == nil {
			t.Fatal("invalid charges")
		}
	}
}

func TestGpuCurrentInstanceContractRejectsRetiredEnvelope(t *testing.T) {
	plan, _, _ := gpuVector(t)
	business := instanceSettingsBusiness(t)
	business.ComputeType = "gpu"
	business.GpuExecution, business.GpuBusinessSpec = plan.Resolved.Execution, plan.Resolved.BusinessSpec
	payload, err := res.CanonicalBusinessPayload(business)
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	command := GpuCanonical{SchemaVersion: 3, GpuRequest: plan.Request, BusinessPayload: payload, BusinessPayloadDigest: hex.EncodeToString(sum[:]), MeteringVersion: GpuMeteringVersion, QuotaItems: []GpuQuotaItem{{QuotaCode: GpuSharedQuotaCode, Units: 8192}}}
	raw, err := gpuCanonicalJSON(command)
	require.NoError(t, err)
	_, err = DecodeGpuCanonical(raw)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "instance_contract_version")
	for _, version := range []uint32{0, 1, 2, 3} {
		legacy := []byte(strings.Replace(string(raw), "{", `{"instance_contract_version":`+strconv.FormatUint(uint64(version), 10)+`,`, 1))
		_, err = DecodeGpuCanonical(legacy)
		require.Error(t, err)
	}
}
