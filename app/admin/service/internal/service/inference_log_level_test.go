package service

import (
	runtimev1 "github.com/liangzai006/ani-inference-service/api/inference/v1"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestInferenceLogLevelProjectionAndFiltering(t *testing.T) {
	for _, tc := range []struct{ message, want string }{{`{"level":"ERROR","message":"broken"}`, "error"}, {`{"severity":"WARNING"}`, "warn"}, {"INFO 10-22 engine started", "info"}, {"[DEBUG] detail", "debug"}, {"unstructured output", "unknown"}, {"error-rate is zero", "unknown"}} {
		line, keep := ProjectInferenceLog(&runtimev1.RuntimeLogLine{Message: tc.message}, "")
		require.True(t, keep)
		require.Equal(t, tc.want, line.Level)
		require.Equal(t, tc.message, line.Message)
		_, keep = ProjectInferenceLog(&runtimev1.RuntimeLogLine{Message: tc.message}, "fatal")
		require.False(t, keep)
	}
}
