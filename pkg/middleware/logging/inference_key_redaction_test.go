package logging

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/stretchr/testify/require"
	admin "go-wind-admin/api/gen/go/admin/service/v1"
	auditpb "go-wind-admin/api/gen/go/audit/service/v1"
	view "go-wind-admin/api/gen/go/inference/service/v1"
)

func TestInferenceKeyAuditDoesNotRetainCredentials(t *testing.T) {
	var record *auditpb.ApiAuditLog
	srv := khttp.NewServer(khttp.Middleware(Server(WithWriteApiLogFunc(func(_ context.Context, in *auditpb.ApiAuditLog) error { record = in; return nil }))))
	srv.Route("/").POST("/api/v1/inference-keys", func(ctx khttp.Context) error {
		khttp.SetOperation(ctx, admin.OperationInferenceKeyServiceCreateInferenceKey)
		_, err := ctx.Middleware(func(context.Context, interface{}) (interface{}, error) {
			return &view.InferenceKeyCredentials{Secret: "iak_private-response"}, nil
		})(ctx, &view.CreateInferenceKeyRequest{})
		return err
	})
	r := httptest.NewRequest("POST", "/api/v1/inference-keys?secret=iak_private-query", strings.NewReader(`{"secret":"iak_private-body"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Referer", "https://example.test/?secret=iak_private-referer")
	srv.ServeHTTP(httptest.NewRecorder(), r)
	require.NotNil(t, record)
	require.Equal(t, "[redacted]", record.GetRequestBody())
	require.Equal(t, "/api/v1/inference-keys", record.GetRequestUri())
	require.Empty(t, record.GetReferer())
	require.Empty(t, record.GetResponse())
}
