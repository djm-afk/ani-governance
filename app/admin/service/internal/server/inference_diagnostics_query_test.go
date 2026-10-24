package server

import (
	"github.com/stretchr/testify/require"
	view "go-wind-admin/api/gen/go/inference/service/v1"
	"net/http/httptest"
	"testing"
)

func TestInferenceDiagnosticQueryValidation(t *testing.T) {
	for _, query := range []string{"page=0", "page=-1", "pageSize=101", "page=1&page=2", "unknown=1", "type=Normal&type=Warning", "since=invalid"} {
		require.Error(t, decodeInferenceDiagnosticsQuery(httptest.NewRequest("GET", "/events?"+query, nil), "service-id", new(view.ListRuntimeEventsRequest)), query)
	}
	logs := new(view.RuntimeLogsRequest)
	require.NoError(t, decodeInferenceDiagnosticsQuery(httptest.NewRequest("GET", "/logs?instance=pod&level=warn", nil), "service-id", logs))
	require.Equal(t, "warn", logs.Level)
	require.Error(t, decodeInferenceDiagnosticsQuery(httptest.NewRequest("GET", "/logs?level=invalid", nil), "service-id", new(view.RuntimeLogsRequest)))
	for _, q := range []string{"step=0", "step=01", "step=3601", "tenant=forged", "start=invalid", "start=2026-10-22T00:00:00Z&start=2026-10-22T00:01:00Z"} {
		require.Error(t, decodeInferenceDiagnosticsQuery(httptest.NewRequest("GET", "/metrics?"+q, nil), "id", new(view.InferenceMetricsRequest)), q)
	}
}
