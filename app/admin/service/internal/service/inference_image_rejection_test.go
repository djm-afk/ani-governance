package service

import (
	"context"
	"testing"

	modelv1 "github.com/liangzai006/ani-model-service/api/model/v1"
	"github.com/stretchr/testify/require"
	"go-wind-admin/app/admin/service/internal/commandcontract"
	"go-wind-admin/app/admin/service/internal/data"
	"go-wind-admin/pkg/middleware/auth"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func inferenceRejectionClaim(t *testing.T) *data.ClaimedOperation {
	t.Helper()
	intent := inferencePublicIntent()
	b := NewInferenceComputeBinding(nil, nil, &inferenceModelProbe{response: &modelv1.GetModelVersionResponse{Version: &modelv1.ModelVersion{Id: intent.ModelVersionId, Status: "ready"}}}, &inferenceComputeProbe{selection: inferenceCPUSelection(t)})
	prepared, err := b.PrepareManagedBusiness(context.Background(), &auth.Principal{Type: auth.SubjectUser, ID: 7, TenantID: 42}, "11111111-1111-4111-8111-111111111111", intent)
	require.NoError(t, err)
	op := &data.ClaimedOperation{OperationID: "20000000-0000-4000-8000-000000000001", ResourceID: "20000000-0000-4000-8000-000000000002", ResourceTenantID: "11111111-1111-4111-8111-111111111111", OwnerService: commandcontract.InferenceOwner, Action: InferenceCreateAction}
	raw, err := prepared.freeze(op.ResourceTenantID, "user", "7", op.OperationID, op.ResourceID, nil)
	require.NoError(t, err)
	op.CanonicalRequest = string(raw)
	return op
}

func imageRejectionInfo(op *data.ClaimedOperation, digest string) *errdetails.ErrorInfo {
	return &errdetails.ErrorInfo{Domain: "ani-inference", Reason: "IMAGE_ADMISSION_REJECTED", Metadata: map[string]string{"stage": "pre_accept", "operation_id": op.OperationID, "resource_id": op.ResourceID, "tenant_id": op.ResourceTenantID, "request_digest": digest, "image_reason": "IMAGE_UNREGISTERED"}}
}

func inferenceRejectionSignal(t *testing.T, op *data.ClaimedOperation, mutate func(*errdetails.ErrorInfo)) error {
	t.Helper()
	c, err := commandcontract.DecodeInferenceCanonical([]byte(op.CanonicalRequest))
	require.NoError(t, err)
	info := &errdetails.ErrorInfo{Domain: "ani-inference", Reason: "IMAGE_ADMISSION_REJECTED", Metadata: map[string]string{"stage": "pre_accept", "operation_id": op.OperationID, "resource_id": op.ResourceID, "tenant_id": op.ResourceTenantID, "request_digest": c.CreateCommandDigest, "image_reason": "IMAGE_UNREGISTERED"}}
	if mutate != nil {
		mutate(info)
	}
	st, err := status.New(codes.FailedPrecondition, "IMAGE_ADMISSION_REJECTED").WithDetails(info)
	require.NoError(t, err)
	return st.Err()
}

func TestInferenceImageRejectionRequiresExactPreacceptSignal(t *testing.T) {
	op := inferenceRejectionClaim(t)
	reason, _, known := inferenceImageRejected(op, inferenceRejectionSignal(t, op, nil))
	require.True(t, known)
	require.Equal(t, "IMAGE_UNREGISTERED", reason)
	for _, field := range []string{"stage", "operation_id", "resource_id", "tenant_id", "request_digest", "image_reason"} {
		t.Run(field, func(t *testing.T) {
			_, _, known := inferenceImageRejected(op, inferenceRejectionSignal(t, op, func(info *errdetails.ErrorInfo) { info.Metadata[field] = "wrong" }))
			require.False(t, known)
		})
	}
	for _, code := range []codes.Code{codes.DeadlineExceeded, codes.Unavailable, codes.NotFound, codes.FailedPrecondition} {
		_, _, known := inferenceImageRejected(op, status.Error(code, "IMAGE_ADMISSION_REJECTED"))
		require.False(t, known, "generic RPC status cannot establish non-acceptance")
	}
}

func TestInferenceImageIntentLegacyHashAndSelectionConflict(t *testing.T) {
	b := &InferenceGpuBinding{}
	intent := inferencePublicIntent()
	tenant := "11111111-1111-4111-8111-111111111111"
	legacy, err := b.IntentHash(tenant, "user", "7", intent)
	require.NoError(t, err)
	// Fixed pre-image snapshot for the original public DTO at origin/dev
	// 30a7e2d5; appended optional fields must not change its digest.
	require.Equal(t, "e669c60f984a5b9ef2325b19dbf4b88cc2f8ec77a26ee1f591179e708d603ad2", legacy)
	intent.ImageId = "img_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	intent.ImageScope = "tenant"
	_, err = b.IntentHash(tenant, "user", "7", intent)
	require.Error(t, err, "raw image and catalog selection are mutually exclusive")
	intent.EngineImage = ""
	chosen, err := b.IntentHash(tenant, "user", "7", intent)
	require.NoError(t, err)
	require.NotEqual(t, legacy, chosen)
	intent.ImageScope = "platform"
	platform, err := b.IntentHash(tenant, "user", "7", intent)
	require.NoError(t, err)
	require.NotEqual(t, chosen, platform)
	intent.ImageScope = ""
	_, err = b.IntentHash(tenant, "user", "7", intent)
	require.Error(t, err)
}
