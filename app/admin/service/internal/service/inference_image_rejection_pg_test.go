//go:build quota_pg

package service

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	runtimev1 "github.com/liangzai006/ani-inference-service/api/inference/v1"
	"github.com/stretchr/testify/require"
	commandv1 "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/command/v1"
	acc "github.com/zhangzhe-ctrl/ani-accelerator-service/api/gen/go/accelerator/v1"
	compute "github.com/zhangzhe-ctrl/ani-resource-service/api/compute/v1"
	view "go-wind-admin/api/gen/go/inference/service/v1"
	"go-wind-admin/app/admin/service/internal/commandcontract"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/app/admin/service/internal/data/ent/quotaoperation"
	"go-wind-admin/app/admin/service/tests/testutil"
	appViewer "go-wind-admin/pkg/entgo/viewer"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The external owner boundary returns only the agreed pre-accept rejection or
// a durable DELETE receipt. PostgreSQL, ledger and worker recovery stay real.
type inferenceRejectedOwner struct {
	directInferenceOwnerProbe
	t                        *testing.T
	createCalls, deleteCalls int
	unknown                  bool
}

func (p *inferenceRejectedOwner) CreateTrusted(_ context.Context, tenant, actor string, req *runtimev1.CreateInferenceServiceRequest) (*runtimev1.OperationResponse, error) {
	p.createCalls++
	if p.unknown {
		return nil, status.Error(codes.DeadlineExceeded, "unknown result")
	}
	op := &data.ClaimedOperation{OperationID: req.Context.OperationId, ResourceID: req.Context.ResourceId, ResourceTenantID: tenant, OwnerService: commandcontract.InferenceOwner, Action: InferenceCreateAction}
	// Use the exact persisted command, avoiding a separate owner state machine.
	st, err := status.New(codes.FailedPrecondition, "IMAGE_ADMISSION_REJECTED").WithDetails(imageRejectionInfo(op, req.Context.RequestDigest))
	require.NoError(p.t, err)
	return nil, st.Err()
}
func (p *inferenceRejectedOwner) DeleteTrusted(_ context.Context, tenant, actor string, req *runtimev1.DeleteInferenceServiceRequest) (*runtimev1.OperationResponse, error) {
	p.deleteCalls++
	return &runtimev1.OperationResponse{Operation: &runtimev1.Operation{Id: req.RequestId, ServiceId: req.ResourceId}, DurableOwnerAck: &commandv1.DurableOwnerAck{OperationId: req.RequestId, ResourceId: req.ResourceId, Accepted: true}}, nil
}

func TestInferenceImageRejectPostgresDurableCloseCPUAndGPU(t *testing.T) {
	for _, scenario := range []struct {
		name                         string
		gpu, unknown, userCloseFirst bool
	}{{"CPU", false, false, false}, {"GPU", true, false, false}, {"CPUUnknown", false, true, false}, {"GPUUnknown", true, true, false}, {"CPUUserCloseFirst", false, false, true}, {"GPUUserCloseFirst", true, false, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			gpu := scenario.gpu
			f := newInferenceGPUFixture(t)
			sys := appViewer.NewSystemViewerContext(context.Background())
			principal, err := auth.PrincipalFromContext(f.ctx)
			require.NoError(t, err)
			tenant := f.model.response.Model.TenantId
			intent := inferencePublicIntent()
			intent.ModelVersionId = f.model.response.Version.Id
			selection := inferenceCPUSelection(t)
			var plan *acc.ResolvedGpuRequest
			items := []data.QuotaOccupyItem{}
			if gpu {
				fixture, _, _ := gpuVector(t)
				fixture.Request.ContainerName = "kserve-container"
				selection.ComputeType = "gpu"
				selection.Gpu = &compute.GpuSelection{ProfileId: fixture.Request.ProfileId, ProfileVersion: fixture.Request.ProfileVersion, SpecDigest: fixture.Profile.SpecDigest, DevicesPerUnit: 1, ClusterId: fixture.Request.ClusterId, PoolId: fixture.Request.PoolId, Mode: fixture.Resolved.Selection.Mode, ModelKey: fixture.Resolved.Selection.ModelKey, MemoryMibPerDevice: 8192, CoreLimitPercent: 25}
				selection.SelectionDigest, err = compute.SelectionDigest(selection)
				require.NoError(t, err)
				plan = fixture.Resolved
				items = append(items, data.QuotaOccupyItem{QuotaCode: GpuSharedQuotaCode, Units: 8192})
			}
			owner := &inferenceRejectedOwner{t: t, unknown: scenario.unknown}
			oldBinding := f.service.acceptance.(*GpuAcceptance).binding.(*InferenceGpuBinding)
			binding := NewInferenceComputeBinding(owner, oldBinding.authorization, f.model, &inferenceComputeProbe{selection: selection})
			prepared, err := binding.PrepareManagedBusiness(f.ctx, principal, tenant, intent)
			require.NoError(t, err)
			createID, resourceID := uuid.NewString(), uuid.NewString()
			actorID := strconv.FormatUint(uint64(principal.ID), 10)
			raw, err := prepared.freeze(tenant, "user", actorID, createID, resourceID, plan)
			require.NoError(t, err)
			accepted, err := f.ledger.Occupy(f.ctx, &data.QuotaOccupyInput{TenantID: f.tenantID, ResourceTenantID: tenant, ResourceID: resourceID, OperationID: createID, ActorType: "user", ActorID: actorID, OwnerService: commandcontract.InferenceOwner, Action: InferenceCreateAction, IdempotencyKey: uuid.NewString(), RequestHash: "image-reject", CanonicalRequest: string(raw), Items: items})
			require.NoError(t, err)
			registry := NewQuotaAdapterRegistry()
			require.NoError(t, registry.Register(binding))
			worker := NewQuotaDispatchWorker(testutil.NewBootstrapContext(nil), f.ledger, registry)
			claims, err := f.ledger.ClaimDispatchable(sys, "first-process", time.Minute, 1)
			require.NoError(t, err)
			require.Len(t, claims, 1)
			userCloseID := ""
			if scenario.userCloseFirst {
				before, err := f.service.DeleteInference(f.ctx, &view.DeleteInferenceRequest{IdempotencyKey: uuid.NewString(), ResourceId: resourceID})
				require.NoError(t, err)
				userCloseID = before.OperationId
			}
			worker.dispatchOne(sys, &claims[0])
			original, err := f.ledger.GetOperationForUser(sys, f.tenantID, accepted.OperationID)
			require.NoError(t, err)
			if scenario.unknown {
				require.Equal(t, quotaoperation.DispatchStateUnknown, original.DispatchState)
				require.False(t, original.RetryBlocked)
				require.Nil(t, original.AckJSON)
				closes, err := f.client.QuotaOperation.Query().Where(quotaoperation.TenantIDEQ(f.tenantID), quotaoperation.CreateOperationIDEQ(createID)).Count(sys)
				require.NoError(t, err)
				require.Zero(t, closes)
				charges, err := f.ledger.GetChargesForOperation(sys, f.tenantID, createID)
				require.NoError(t, err)
				for _, charge := range charges {
					require.Zero(t, charge.ReleasedUnits)
				}
				return
			}

			require.Equal(t, quotaoperation.DispatchStateRejected, original.DispatchState)
			require.True(t, original.RetryBlocked)
			var saved data.InferenceImageRejection
			require.NoError(t, json.Unmarshal([]byte(*original.AckJSON), &saved))
			require.Equal(t, "IMAGE_UNREGISTERED", saved.Reason)
			require.NotEmpty(t, saved.CloseOperationID)
			if userCloseID != "" {
				require.Equal(t, userCloseID, saved.CloseOperationID)
			}
			lateACK, err := f.ledger.AckDispatched(sys, f.tenantID, createID, claims[0].LeaseGeneration, `{"accepted":true}`)
			require.NoError(t, err)
			require.False(t, lateACK, "a late CREATE ACK cannot replace the persisted rejection")
			repeated, err := f.ledger.RejectInferenceImageAndClose(sys, &claims[0], saved.Reason, saved.Message)
			require.NoError(t, err)
			require.Equal(t, saved.CloseOperationID, repeated.OperationID)
			closeRow, err := f.ledger.GetOperationForUser(sys, f.tenantID, saved.CloseOperationID)
			require.NoError(t, err)
			// A user close that read CREATE before rejection must also converge
			// when its actual ledger transaction begins after the P1 commit.
			staleClose, err := f.ledger.CreateDeleteOperation(sys, &data.QuotaDeleteInput{TenantID: f.tenantID, ActorType: "user", ActorID: actorID, OwnerService: commandcontract.InferenceOwner, Action: InferenceDeleteAction, IdempotencyKey: uuid.NewString(), RequestHash: "concurrent-user-close", ResourceID: resourceID, CanonicalRequest: closeRow.CanonicalRequest})
			require.NoError(t, err)
			require.Equal(t, saved.CloseOperationID, staleClose.OperationID)
			// User close after the automated intent reuses the same operation.
			closeResult, err := f.service.DeleteInference(f.ctx, &view.DeleteInferenceRequest{IdempotencyKey: uuid.NewString(), ResourceId: resourceID})
			require.NoError(t, err)
			require.Equal(t, saved.CloseOperationID, closeResult.OperationId)
			// A new worker instance resumes the durable intent after restart.
			restarted := NewQuotaDispatchWorker(testutil.NewBootstrapContext(nil), f.ledger, registry)
			closeClaims, err := f.ledger.ClaimDispatchable(sys, "restarted-process", time.Minute, 10)
			require.NoError(t, err)
			require.Len(t, closeClaims, 1)
			require.Equal(t, saved.CloseOperationID, closeClaims[0].OperationID)
			require.Equal(t, createID, closeClaims[0].CreateOperationID)
			restarted.dispatchOne(sys, &closeClaims[0])
			closed, err := f.ledger.GetOperationForUser(sys, f.tenantID, saved.CloseOperationID)
			require.NoError(t, err)
			require.Equal(t, quotaoperation.DispatchStateAcked, closed.DispatchState)
			charges, err := f.ledger.GetChargesForOperation(sys, f.tenantID, createID)
			require.NoError(t, err)
			for _, charge := range charges {
				require.Zero(t, charge.ReleasedUnits, "DELETE ACK must never refund original charge")
			}
			if gpu {
				release := &data.QuotaReleaseInput{OwnerService: commandcontract.InferenceOwner, OperationID: createID, ResourceID: resourceID, Complete: true, ReleaseEventID: uuid.NewString(), Reason: "ABORTED_CLEANED", PayloadHash: "owner-release-evidence", PayloadJSON: `{"evidence":"external-owner-boundary"}`}
				for attempt := 0; attempt < 2; attempt++ {
					_, err = f.ledger.Release(sys, release)
					require.NoError(t, err)
				}
				refunded, err := f.ledger.GetChargesForOperation(sys, f.tenantID, createID)
				require.NoError(t, err)
				for _, charge := range refunded {
					require.Equal(t, charge.OriginalUnits, charge.ReleasedUnits)
				}
			}

			require.Equal(t, 1, owner.createCalls)
			require.Equal(t, 1, owner.deleteCalls)
			visible, err := f.service.GetInferenceOperation(f.ctx, &view.GetInferenceOperationRequest{OperationId: createID})
			require.NoError(t, err)
			require.Equal(t, "IMAGE_UNREGISTERED", visible.ErrorCode)
			require.Equal(t, saved.CloseOperationID, visible.CloseOperationId)
		})
	}
}
