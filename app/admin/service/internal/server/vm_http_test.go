package server

import (
	kratoserrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

func TestVMHTTPErrorPreservesVMContractReasons(t *testing.T) {
	withReason, err := status.New(codes.FailedPrecondition, "private native detail").WithDetails(&errdetails.ErrorInfo{Reason: "VM_DEPENDENCY_CHANGED"})
	require.NoError(t, err)
	for _, test := range []struct {
		err    error
		code   int
		reason string
	}{
		{status.Error(codes.Aborted, "VM_VERSION_CONFLICT"), 409, "VM_VERSION_CONFLICT"},
		{status.Error(codes.NotFound, "VM_NOT_FOUND"), 404, "VM_NOT_FOUND"},
		{withReason.Err(), 409, "VM_DEPENDENCY_CHANGED"},
		{kratoserrors.Conflict("IDEMPOTENCY_CONFLICT", "private ledger details"), 409, "VM_IDEMPOTENCY_CONFLICT"},
		{status.Error(codes.Unavailable, "private address"), 503, "VM_DEPENDENCY_NOT_READY"},
	} {
		result := vmHTTPError(test.err)
		require.Equal(t, test.code, kratoserrors.Code(result))
		require.Equal(t, test.reason, kratoserrors.Reason(result))
		require.NotContains(t, result.Error(), "private")
	}
}
