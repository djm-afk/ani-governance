package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	"os"
	"testing"

	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Inputs came from the real ACC BuildPlan. Whole1/2 share a max=2 Profile;
// WholeProfileMax exercises a higher Profile boundary. No hardware is implied.
func TestGpuQuantityBuilderVectorsAndOriginalCharges(t *testing.T) {
	raw, err := os.ReadFile("testdata/accelerator-quantity-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors map[string]json.RawMessage
	if err = json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	for name, raw := range vectors {
		t.Run(name, func(t *testing.T) {
			p := new(acc.ResolvedGpuPlan)
			if err := protojson.Unmarshal(raw, p); err != nil {
				t.Fatal(err)
			}
			quota, err := GpuPlanQuota(p)
			if err != nil {
				t.Fatal(err)
			}
			expected := int64(p.Request.Replicas) * int64(p.Request.DevicesPerReplica)
			if name == "Shared1" {
				expected = int64(p.Request.Replicas) * p.Profile.Spec.SharedMemoryMib
			}
			if quota.Units != expected {
				t.Fatalf("charge %d expected %d", quota.Units, expected)
			}
			payload := json.RawMessage(`{"quantity_contract":true}`)
			sum := sha256.Sum256(payload)
			digest := hex.EncodeToString(sum[:])
			c := &GpuCanonical{SchemaVersion: 2, GpuRequest: p.Request, GpuPlan: p, BusinessPayload: payload, BusinessPayloadDigest: digest, MeteringVersion: GpuMeteringVersion, QuotaItems: []GpuQuotaItem{{QuotaCode: quota.QuotaCode, Units: quota.Units}}}
			encoded, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := DecodeGpuCanonical(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(decoded.GpuPlan, p) {
				t.Fatal("full plan lost on canonical round trip")
			}
			refs, err := GpuChargeSubset(decoded, []QuotaChargeRef{{ChargeID: "original", QuotaCode: quota.QuotaCode, ChargedUnits: expected}})
			if err != nil || len(refs) != 1 || refs[0].OriginalUnits != expected {
				t.Fatal(refs, err)
			}
			if name == "WholeProfileMax" {
				if p.Request.DevicesPerReplica <= 2 || p.Request.DevicesPerReplica != p.Profile.Spec.MaxDevicesPerReplica {
					t.Fatal("missing higher Profile boundary")
				}
				business := instanceSettingsBusiness(t)
				business.Gpu = proto.Clone(p.Request).(*acc.GpuRequest)
				business.Spec.ComputeType = "gpu"
				business.Spec.ClusterId = p.Request.ClusterId
				business.Spec.GpuSpecDigest = proto.String(p.Profile.SpecDigest)
				business.Spec.GpuSummary = &res.GpuSummary{ProfileId: p.Request.ProfileId, ProfileVersion: p.Request.ProfileVersion, Mode: p.Profile.Spec.Mode, ModelKey: p.Profile.Spec.ModelKey, DevicesPerInstance: p.Request.DevicesPerReplica, CoreLimitPercent: p.Profile.Spec.CoreLimitPercent}
				if _, err = NewInstanceGpuBinding(nil, nil, p.Request.ClusterId).ValidateGpuBusiness(context.Background(), business); err != nil {
					t.Fatal(err)
				}
				over := proto.Clone(p).(*acc.ResolvedGpuPlan)
				over.Profile.Spec.MaxDevicesPerReplica = over.Request.DevicesPerReplica - 1
				over.Profile.SpecDigest, err = gpuSpecDigest(over.Profile)
				if err != nil {
					t.Fatal(err)
				}
				over.ResolutionDigest, err = GpuPlanDigest(over)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = GpuPlanQuota(over); err == nil {
					t.Fatal("request above Profile max accepted")
				}
			}
			for _, mutate := range []func(*acc.ResolvedGpuPlan){func(p *acc.ResolvedGpuPlan) { p.Totals.ExclusiveDeviceCount++ }, func(p *acc.ResolvedGpuPlan) {
				for _, v := range p.Runtime.LimitsPerContainer {
					if v.Key == "volcano.sh/vgpu-number" {
						v.Value = "0"
					}
				}
			}} {
				changed := proto.Clone(p).(*acc.ResolvedGpuPlan)
				mutate(changed)
				changed.ResolutionDigest, err = GpuPlanDigest(changed)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = GpuPlanQuota(changed); err == nil {
					t.Fatal("quantity mutation accepted after digest recomputation")
				}
			}
		})
	}
}
