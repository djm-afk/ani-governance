package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	errors "github.com/go-kratos/kratos/v2/errors"
	"testing"

	contractv1 "github.com/liangzai006/ani-inference-service/api/inference/contract/v1"
	runtimev1 "github.com/liangzai006/ani-inference-service/api/inference/v1"
	modelv1 "github.com/liangzai006/ani-model-service/api/model/v1"
	"github.com/stretchr/testify/require"
	compute "github.com/zhangzhe-ctrl/ani-resource-service/api/compute/v1"
	"go-wind-admin/app/admin/service/internal/commandcontract"
	"go-wind-admin/pkg/middleware/auth"
)

func TestInferenceNewGpuRejectedBeforeOwnerOrQuota(t *testing.T) {
	fixture, _, _ := gpuVector(t)
	selection := inferenceCPUSelection(t)
	g := fixture.Resolved.BusinessSpec
	selection.ComputeType = "gpu"
	selection.Gpu = &compute.GpuSelection{ProfileId: g.ProfileId, ProfileVersion: g.ProfileVersion, SpecDigest: g.SpecDigest, DevicesPerUnit: g.DevicesPerReplica, ClusterId: fixture.Request.ClusterId, PoolId: fixture.Request.PoolId, Mode: g.Mode, ModelKey: g.ModelKey, MemoryMibPerDevice: g.MemoryMibPerDevice, CoreLimitPercent: g.CoreLimitPercent, AllocationType: g.AllocationType, ResourceKey: g.ResourceKey, ComputeUnitsPerDevice: g.ComputeUnitsPerDevice, AdapterId: g.AdapterId}
	var err error
	selection.SelectionDigest, err = compute.SelectionDigest(selection)
	require.NoError(t, err)
	binding := NewInferenceComputeBinding(nil, nil, nil, &inferenceComputeProbe{selection: selection})
	prepared, err := binding.PrepareManagedBusiness(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7, TenantID: 42}, "11111111-1111-4111-8111-111111111111", inferencePublicIntent())
	require.Nil(t, prepared)
	require.Error(t, err)
	require.Equal(t, "QUOTA_ADAPTER_UNAVAILABLE", errors.Reason(err))
	raw, err := commandcontract.CanonicalJSON(commandcontract.InferenceCanonical{ResourceClass: commandcontract.InferenceGPUClass, Selection: selection})
	require.NoError(t, err)
	_, err = commandcontract.DecodeInferenceCanonical(raw)
	require.Equal(t, "QUOTA_ADAPTER_UNAVAILABLE", errors.Reason(err))
}

func TestInferenceStoredCommandVariantsAndDeleteScope(t *testing.T) {
	t.Run("CPU", func(t *testing.T) {
		intent := inferencePublicIntent()
		selection := inferenceCPUSelection(t)
		b := NewInferenceComputeBinding(nil, nil, &inferenceModelProbe{response: &modelv1.GetModelVersionResponse{Version: &modelv1.ModelVersion{Id: intent.ModelVersionId, Status: "ready"}}}, &inferenceComputeProbe{selection: selection})
		tenant := "11111111-1111-4111-8111-111111111111"
		createID := "20000000-0000-4000-8000-000000000001"
		resourceID := "20000000-0000-4000-8000-000000000002"
		prepared, e := b.PrepareManagedBusiness(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7, TenantID: 42}, tenant, intent)
		require.NoError(t, e)
		raw, e := prepared.freeze(tenant, "user", "7", createID, resourceID, nil)
		require.NoError(t, e)
		in := commandcontract.StoredCommand{Owner: commandcontract.InferenceOwner, Action: InferenceCreateAction, TenantID: tenant, ResourceID: resourceID, OperationID: createID, ActorType: "user", ActorID: "7", Canonical: raw, RequireChargeIDs: true}
		facts, e := commandcontract.ValidateStoredCommand(in)
		require.NoError(t, e)
		require.True(t, facts.CPU)
		wrong := in
		wrong.ActorID = "8"
		_, e = commandcontract.ValidateStoredCommand(wrong)
		require.Error(t, e)
		wrong = in
		wrong.Owner = InstanceOwner
		_, e = commandcontract.ValidateStoredCommand(wrong)
		require.Error(t, e)
		wrong = in
		wrong.TenantID = "11111111-1111-4111-8111-111111111112"
		_, e = commandcontract.ValidateStoredCommand(wrong)
		require.Error(t, e)
		wrong = in
		wrong.Charges = []commandcontract.ChargeRef{{ChargeID: "fake", QuotaCode: GpuSharedQuotaCode, ChargedUnits: 8192}}
		_, e = commandcontract.ValidateStoredCommand(wrong)
		require.Error(t, e)
		c, e := commandcontract.DecodeInferenceCanonical(raw)
		require.NoError(t, e)
		c.DeleteScope = &runtimev1.ManagedDeleteScope{ResourceClass: c.ResourceClass, ClusterId: c.ClusterID, CreateCommandDigest: c.CreateCommandDigest}
		deleted, e := commandcontract.CanonicalJSON(c)
		require.NoError(t, e)
		_, e = commandcontract.DeleteSnapshot(commandcontract.InferenceOwner, string(raw), string(deleted))
		require.NoError(t, e)
		in.Action = InferenceDeleteAction
		in.CreateOperationID = createID
		in.OperationID = "20000000-0000-4000-8000-000000000003"
		in.ActorID = "8"
		in.Canonical = deleted
		_, e = commandcontract.ValidateStoredCommand(in)
		require.NoError(t, e, "a newly authorized actor may close the original resource")
		c.DeleteScope.CreateCommandDigest = "forged"
		mutated, e := commandcontract.CanonicalJSON(c)
		require.NoError(t, e)
		_, e = commandcontract.DeleteSnapshot(commandcontract.InferenceOwner, string(raw), string(mutated))
		require.Error(t, e)
		c.DeleteScope.CreateCommandDigest = c.CreateCommandDigest
		business, e := contractv1.DecodeBusinessPayload(c.BusinessPayload)
		require.NoError(t, e)
		business.Engine.Command = "changed"
		c.BusinessPayload, e = contractv1.CanonicalBusinessPayload(business)
		require.NoError(t, e)
		sum := sha256.Sum256(c.BusinessPayload)
		c.BusinessPayloadDigest = hex.EncodeToString(sum[:])
		mutated, e = commandcontract.CanonicalJSON(c)
		require.NoError(t, e)
		_, e = commandcontract.DeleteSnapshot(commandcontract.InferenceOwner, string(raw), string(mutated))
		require.Error(t, e, "DELETE cannot rewrite frozen business")
	})
}
