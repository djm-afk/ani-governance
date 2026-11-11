package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	res "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	"go-wind-admin/app/admin/service/internal/commandcontract"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/protobuf/proto"
)

// Instance identity and persistence use shared admission. Only this binding
// interprets Resource's CreateSpec and preserves its current signed bytes.
func (b *InstanceGpuBinding) PrepareManagedBusiness(ctx context.Context, p *auth.Principal, tenant string, public proto.Message) (*preparedManagedBusiness, error) {
	gpu, message, e := b.ResolveGpuBusiness(ctx, p, tenant, public)
	if e != nil {
		return nil, e
	}
	business, ok := message.(*res.CreateSpec)
	if !ok || business == nil {
		return nil, commandcontract.Invalid("invalid instance contract")
	}
	if gpu != nil {
		if _, e = b.ValidateGpuBusiness(ctx, business); e != nil {
			return nil, e
		}
	}
	return &preparedManagedBusiness{gpu: gpu, freeze: func(_, _, _, _, _ string, plan *acc.ResolvedGpuRequest) ([]byte, error) {
		frozen := proto.Clone(business).(*res.CreateSpec)
		if gpu == nil {
			if plan != nil || frozen.ComputeType != "cpu" || frozen.GpuSelection != nil || frozen.GpuExecution != nil || frozen.GpuBusinessSpec != nil {
				return nil, commandcontract.Invalid("invalid CPU instance resolution")
			}
			raw, e := res.CanonicalBusinessPayload(frozen)
			if e != nil {
				return nil, e
			}
			digest, e := res.BusinessPayloadDigest(frozen)
			if e != nil {
				return nil, e
			}
			return commandcontract.MarshalInstanceCPUCanonical(&commandcontract.InstanceCPUCanonical{Schema: commandcontract.InstanceCPUCanonicalSchema, BusinessPayload: raw, BusinessPayloadDigest: digest})
		}
		item, e := commandcontract.GpuResolvedQuota(plan, gpu)
		if e != nil {
			return nil, e
		}
		if e = b.ValidateResolvedInstancePlan(ctx, frozen, plan); e != nil {
			return nil, e
		}
		frozen.GpuExecution = proto.Clone(plan.Execution).(*acc.GpuExecution)
		if e = validateResolvedInstanceReference(frozen.GpuSelection, plan.BusinessSpec); e != nil {
			return nil, e
		}
		frozen.GpuBusinessSpec = proto.Clone(plan.BusinessSpec).(*acc.GpuBusinessSpec)
		frozen.GpuSelection = nil
		if e = res.ValidateCreateSpec(frozen, true); e != nil {
			return nil, instanceBusinessError(e)
		}
		raw, e := res.CanonicalBusinessPayload(frozen)
		if e != nil {
			return nil, e
		}
		sum := sha256.Sum256(raw)
		return commandcontract.CanonicalJSON(commandcontract.GpuCanonical{SchemaVersion: 3, GpuRequest: gpu, BusinessPayload: raw, BusinessPayloadDigest: hex.EncodeToString(sum[:]), MeteringVersion: commandcontract.GpuMeteringVersion, QuotaItems: []commandcontract.GpuQuotaItem{{QuotaCode: item.QuotaCode, Units: item.Units}}})
	}}, nil
}
func (b *InstanceGpuBinding) instanceIntentHash(tenant, actorType, actorID string, gpu *acc.GpuRequest, business proto.Message) (string, error) {
	raw, e := b.CanonicalGpuBusiness(business)
	if e != nil {
		return "", e
	}
	var object map[string]any
	if e = json.Unmarshal(raw, &object); e != nil {
		return "", e
	}
	var gpuObject map[string]any
	if gpu != nil {
		gpuObject, e = commandcontract.CanonicalMessage(gpu.ProtoReflect())
		if e != nil {
			return "", e
		}
	}
	return commandcontract.Hash(map[string]any{"schema": "gov-instance-create", "tenant_id": tenant, "actor_type": actorType, "actor_id": actorID, "owner": b.OwnerService(), "action": b.CreateAction(), "gpu_request": gpuObject, "business_type": string(business.ProtoReflect().Descriptor().FullName()), "business": object})
}

func validateResolvedInstanceReference(g *res.GpuPresetReference, s *acc.GpuBusinessSpec) error {
	if g == nil || s == nil || g.ProfileId != s.ProfileId || g.ProfileVersion != s.ProfileVersion || g.SpecDigest != s.SpecDigest || g.DevicesPerInstance != s.DevicesPerReplica || g.Mode != s.Mode || g.ModelKey != s.ModelKey || g.AllocationType != s.AllocationType || g.MemoryMibPerDevice != s.MemoryMibPerDevice || g.CoreLimitPercent != s.CoreLimitPercent || g.ComputeUnitsPerDevice != s.ComputeUnitsPerDevice || g.ResourceKey != s.ResourceKey || g.AdapterId != s.AdapterId {
		return commandcontract.Invalid("resolved GPU differs from managed preset reference")
	}
	return nil
}
