package service

import (
	"encoding/json"
	"strings"

	"go-wind-admin/app/admin/service/internal/commandcontract"
	"go-wind-admin/app/admin/service/internal/data"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// This signal is consumed only after the registered Inference mTLS adapter has
// returned it for this exact frozen CREATE. Generic RPC errors remain unknown.
func inferenceImageRejected(op *data.ClaimedOperation, err error) (reason, message string, known bool) {
	if op == nil || op.OwnerService != commandcontract.InferenceOwner || op.Action != commandcontract.InferenceCreateAction || op.CreateOperationID != "" {
		return "", "", false
	}
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.FailedPrecondition {
		return "", "", false
	}
	c, e := commandcontract.DecodeInferenceCanonical([]byte(op.CanonicalRequest))
	if e != nil {
		return "", "", false
	}
	var signal *errdetails.ErrorInfo
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			if signal != nil {
				return "", "", false
			}
			signal = info
		}
	}
	if signal == nil || signal.Domain != "ani-inference" || signal.Reason != "IMAGE_ADMISSION_REJECTED" {
		return "", "", false
	}
	m := signal.Metadata
	if m["stage"] != "pre_accept" || m["operation_id"] != op.OperationID || m["resource_id"] != op.ResourceID || m["tenant_id"] != op.ResourceTenantID || m["request_digest"] != c.CreateCommandDigest || m["image_reason"] == "" {
		return "", "", false
	}
	// Keep bounded structured business diagnostics, never a registry credential
	// or arbitrary transport error. The owner message is a stable public code.
	if st.Message() != "IMAGE_ADMISSION_REJECTED" {
		return "", "", false
	}
	reason = m["image_reason"]
	if len(reason) == 0 || len(reason) > 128 || reason[0] < 'A' || reason[0] > 'Z' || strings.Trim(reason, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") != "" {
		return "", "", false
	}
	return reason, "IMAGE_ADMISSION_REJECTED", true
}

func inferenceRejectedClose(opJSON *string) (data.InferenceImageRejection, bool) {
	var saved data.InferenceImageRejection
	if opJSON == nil || json.Unmarshal([]byte(*opJSON), &saved) != nil || saved.CloseOperationID == "" || saved.Reason == "" {
		return saved, false
	}
	return saved, true
}
