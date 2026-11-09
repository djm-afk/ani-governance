package logging

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/stretchr/testify/require"
	"go-wind-admin/pkg/middleware/auth"
)

func TestClusterOpsSensitiveBodiesNeverEnterAuditSnapshot(t *testing.T) {
	const sentinel = "clusterops-private-secret-yaml-log-sentinel"
	for operation, policy := range auth.ClusterOpsOperations {
		t.Run(strings.TrimPrefix(operation, auth.ClusterOpsOperationPrefix), func(t *testing.T) {
			env := newAuditServer(t)
			env.fire(policy.Method, "/case/5?secret="+sentinel, map[string]string{
				"X-Test-Operation": operation, "Content-Type": "application/json", "Referer": "https://example.test/?secret=" + sentinel,
			}, `{"data":{"manifest_yaml":"`+sentinel+`"}}`, "127.0.0.1:1234")
			require.Len(t, env.capture.api, 1)
			record := env.capture.api[0]
			require.Equal(t, "[redacted]", record.GetRequestBody())
			require.Empty(t, record.GetReferer())
			require.Equal(t, "/case/5", record.GetRequestUri())
			require.NotContains(t, record.String(), sentinel)
		})
	}
	srv := khttp.NewServer(khttp.Middleware(Server()))
	srv.Route("/").POST("/snapshot", func(ctx khttp.Context) error {
		khttp.SetOperation(ctx, auth.ClusterOpsOperationPrefix+"UpdateResource")
		_, err := ctx.Middleware(func(call context.Context, _ any) (any, error) {
			require.Nil(t, bodySnapshotFromContext(call), "sensitive body must never be snapshotted")
			body, err := io.ReadAll(ctx.Request().Body)
			require.NoError(t, err)
			require.Contains(t, string(body), sentinel, "business handler must still receive original bytes")
			return nil, nil
		})(ctx, nil)
		return err
	})
	request := httptest.NewRequest(http.MethodPost, "/snapshot", strings.NewReader(`{"data":{"manifest_yaml":"`+sentinel+`"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	srv.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
}
