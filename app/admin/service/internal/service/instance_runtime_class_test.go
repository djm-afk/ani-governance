package service

import (
	"bytes"
	"testing"

	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	"google.golang.org/protobuf/proto"
)

// The managed owner runtime is resolved by Resource before Gov signs the
// business snapshot. It must survive Gov's canonical/dispatch decode without
// changing the public intent that identifies an original-request replay.
func TestInstanceGpuRuntimeClassFrozenCanonical(t *testing.T) {
	plan, _, _ := gpuVector(t)
	business := instanceSettingsBusiness(t)
	business.Gpu = proto.Clone(plan.Request).(*acc.GpuRequest)
	business.Spec.ComputeType = "gpu"
	business.Spec.ClusterId = plan.Request.ClusterId
	business.Spec.GpuSpecDigest = proto.String(plan.Profile.SpecDigest)
	business.Spec.GpuSummary = &res.GpuSummary{
		ProfileId:          plan.Request.ProfileId,
		ProfileVersion:     plan.Request.ProfileVersion,
		Mode:               plan.Profile.Spec.Mode,
		ModelKey:           plan.Profile.Spec.ModelKey,
		DevicesPerInstance: plan.Request.DevicesPerReplica,
		CoreLimitPercent:   plan.Profile.Spec.CoreLimitPercent,
	}
	publicDigest, err := res.BusinessPayloadDigest(business.Data)
	if err != nil {
		t.Fatal(err)
	}
	absent, err := res.BusinessPayloadDigest(business)
	if err != nil {
		t.Fatal(err)
	}
	business.Spec.GpuRuntimeClassName = proto.String("nvidia")
	binding := NewInstanceGpuBinding(nil, nil, plan.Request.ClusterId)
	raw, err := binding.CanonicalGpuBusiness(business)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"gpu_runtime_class_name":"nvidia"`)) {
		t.Fatal("managed runtime class missing from frozen canonical")
	}
	frozen, err := res.DecodeBusinessPayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Spec.GetGpuRuntimeClassName() != "nvidia" || !proto.Equal(frozen, business) {
		t.Fatal("managed runtime class lost at owner dispatch boundary")
	}
	original, err := res.BusinessPayloadDigest(frozen)
	if err != nil || original == absent {
		t.Fatalf("managed runtime class not bound to business digest: %v", err)
	}
	business.Spec.GpuRuntimeClassName = proto.String("nvidia-next")
	changed, err := res.BusinessPayloadDigest(business)
	if err != nil || changed == original {
		t.Fatalf("new managed runtime not distinguished: %v", err)
	}
	if frozen.Spec.GetGpuRuntimeClassName() != "nvidia" {
		t.Fatal("later configuration mutated the frozen original snapshot")
	}
	currentPublic, err := res.BusinessPayloadDigest(business.Data)
	if err != nil || currentPublic != publicDigest {
		t.Fatalf("managed runtime changed original public intent: %v", err)
	}
}
