package service

import (
	"encoding/json"
	"os"
	"testing"

	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	"google.golang.org/protobuf/proto"
)

// The identical input/digests are checked by Resource's currentBusiness test.
// This catches generated-package drift at the consumer as well as omissions
// from the frozen payload, without replacing admission or persistence.
func TestInstanceSharedCanonicalAndFrozenParameters(t *testing.T) {
	raw, err := os.ReadFile("testdata/instance-canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Business   json.RawMessage `json:"canonical_business"`
		Digest     string          `json:"business_digest"`
		DataDigest string          `json:"raw_data_digest"`
		NoneDigest string          `json:"explicit_none_data_digest"`
	}
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	business, err := res.DecodeBusinessPayload(v.Business)
	if err != nil {
		t.Fatal(err)
	}
	if digest, err := res.BusinessPayloadDigest(business); err != nil || digest != v.Digest {
		t.Fatalf("shared business digest %s: %v", digest, err)
	}
	if digest, err := res.BusinessPayloadDigest(business.Data); err != nil || digest != v.DataDigest {
		t.Fatalf("shared raw data digest %s: %v", digest, err)
	}
	explicit := proto.Clone(business.Data).(*res.CreateInstanceData)
	explicit.Scheduling = &res.SchedulingOptions{PodPlacement: res.SchedulingOptions_NONE.Enum()}
	if digest, err := res.BusinessPayloadDigest(explicit); err != nil || digest != v.NoneDigest || digest == v.DataDigest {
		t.Fatalf("presence lost %s: %v", digest, err)
	}
	mutations := map[string]func(*res.CreateBusiness){
		"cpu": func(b *res.CreateBusiness) { b.Spec.Cpu = "2000m" }, "memory": func(b *res.CreateBusiness) { b.Spec.Memory = "4096Mi" },
		"image": func(b *res.CreateBusiness) { b.Spec.ImageReference += "-changed" }, "cluster": func(b *res.CreateBusiness) { b.Spec.ClusterId = "10000000-0000-4000-8000-000000000001" },
		"namespace": func(b *res.CreateBusiness) { b.Spec.Namespace += "-changed" }, "workspace": func(b *res.CreateBusiness) { b.Spec.WorkspaceSize = "2Gi" },
		"account": func(b *res.CreateBusiness) { b.Spec.ServiceAccount = "other" }, "storage": func(b *res.CreateBusiness) { b.Spec.StorageClass = "other" },
		"uid": func(b *res.CreateBusiness) { b.Spec.Uid++ }, "gid": func(b *res.CreateBusiness) { b.Spec.Gid++ }, "shell": func(b *res.CreateBusiness) { b.Spec.Shell = "/bin/bash" },
		"actual-placement": func(b *res.CreateBusiness) {
			b.Spec.Scheduling.PodPlacement = res.SchedulingOptions_SPREAD_PREFERRED.Enum()
		},
		"actual-priority": func(b *res.CreateBusiness) { name := "normal"; b.Spec.Scheduling.PriorityClassName = &name },
		"original-presence": func(b *res.CreateBusiness) {
			b.Data.Scheduling = &res.SchedulingOptions{PodPlacement: res.SchedulingOptions_NONE.Enum()}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			b := proto.Clone(business).(*res.CreateBusiness)
			mutate(b)
			digest, err := res.BusinessPayloadDigest(b)
			if err != nil || digest == v.Digest {
				t.Fatalf("frozen change omitted %s %v", digest, err)
			}
		})
	}
	for _, pair := range [][2]res.SchedulingOptions_PodPlacement{{res.SchedulingOptions_SPREAD_PREFERRED, res.SchedulingOptions_NONE}, {res.SchedulingOptions_NONE, res.SchedulingOptions_SPREAD_PREFERRED}} {
		b := proto.Clone(business).(*res.CreateBusiness)
		b.Data.Scheduling = &res.SchedulingOptions{PodPlacement: pair[0].Enum()}
		b.Spec.Scheduling.PodPlacement = pair[1].Enum()
		if _, err = res.BusinessPayloadDigest(b); err != nil {
			t.Fatal(err)
		}
		if err = res.ValidateCreateBusiness(b); err == nil {
			t.Fatal("correct digest accepted contradictory scheduling")
		}
	}
}
