package data

import (
	"testing"

	"github.com/stretchr/testify/require"
	modeldevv1 "github.com/zhangzhe-ctrl/ani-modeldev-service/api/ani/modeldev/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A stale expected_revision and a duplicate name are both COMMAND_CONFLICT, but
// the owner distinguishes them by gRPC code. Both must survive sanitization so
// the service layer can answer 409 instead of degrading to 503.
func TestModelDevDatasetErrorAcceptsBothCommandConflictCodes(t *testing.T) {
	for _, code := range []codes.Code{codes.AlreadyExists, codes.Aborted} {
		failure, err := status.New(code, "private owner text").
			WithDetails(&modeldevv1.ErrorDetail{Reason: modeldevv1.ErrorReason_ERROR_REASON_COMMAND_CONFLICT})
		require.NoError(t, err)
		out := modelDevDatasetError(failure.Err())
		var typed *ModelDevDatasetFailure
		require.ErrorAs(t, out, &typed)
		require.Equal(t, code, typed.Code)
		require.Equal(t, modeldevv1.ErrorReason_ERROR_REASON_COMMAND_CONFLICT, typed.Reason)
	}
}

// Every other reason still binds to exactly one code; a mismatched pair is a
// contract violation and must not reach the public mapping.
func TestModelDevDatasetErrorRejectsMismatchedReasonAndCode(t *testing.T) {
	for _, test := range []struct {
		name   string
		code   codes.Code
		reason modeldevv1.ErrorReason
	}{
		{"revision conflict as invalid", codes.InvalidArgument, modeldevv1.ErrorReason_ERROR_REASON_COMMAND_CONFLICT},
		{"not found as aborted", codes.Aborted, modeldevv1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND},
		{"unknown reason", codes.Aborted, modeldevv1.ErrorReason_ERROR_REASON_UNSPECIFIED},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure, err := status.New(test.code, "private owner text").WithDetails(&modeldevv1.ErrorDetail{Reason: test.reason})
			require.NoError(t, err)
			out := modelDevDatasetError(failure.Err())
			var typed *ModelDevDatasetFailure
			require.NotErrorAs(t, out, &typed)
			require.Equal(t, codes.Unavailable, status.Code(out))
		})
	}
}
