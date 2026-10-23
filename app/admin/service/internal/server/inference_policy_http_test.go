package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/middleware"
	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/stretchr/testify/require"
	admin "go-wind-admin/api/gen/go/admin/service/v1"
	view "go-wind-admin/api/gen/go/inference/service/v1"
	"go-wind-admin/app/admin/service/internal/service"
)

func TestInferencePolicyHTTPBoundary(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	path := "/api/v1/inference/services/" + id + "/policy"
	calls := 0
	srv := kratoshttp.NewServer(kratoshttp.Middleware(func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, in interface{}) (interface{}, error) {
			calls++
			switch request := in.(type) {
			case *view.UpdateServicePolicyRequest:
				require.Equal(t, id, request.ResourceId)
				require.NotNil(t, request.Policy)
			case *view.GetServicePolicyRequest:
				require.Equal(t, id, request.ResourceId)
			default:
				t.Fatalf("unexpected request: %T", in)
			}
			return &view.GetServicePolicyResponse{}, nil
		}
	}))
	RegisterInferenceHTTPServer(srv, service.NewInferenceService(nil))
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"GET", path, "", 200},
		{"PUT", path, `{"policy":{"allowed_tenants":[],"rate_limits":{}}}`, 200},
		{"PUT", path, `{"tenant_id":"forged","policy":{}}`, 400},
		{"PUT", path, `{"resource_id":"different","policy":{}}`, 400},
		{"PUT", path, `{"policy":{},"policy":{}}`, 400},
		{"PUT", path, `{"policy":{"rate_limits":{"tenant":{},"tenant":{}}}}`, 400},
		{"PUT", path + "?tenant_id=forged", `{"policy":{}}`, 400},
		{"GET", path + "?tenant_id=forged", "", 400},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		srv.ServeHTTP(response, request)
		require.Equal(t, tc.want, response.Code, "%s %s %s", tc.method, tc.path, tc.body)
	}
	require.Equal(t, 2, calls)
	// The route operations belong to the existing INFERENCE module's middleware.
	require.Contains(t, admin.OperationInferenceServiceGetServicePolicy, "InferenceService/GetServicePolicy")
}
func TestInferencePolicyHTTPRejectsForgedIdentity(t *testing.T) {
	srv := kratoshttp.NewServer()
	RegisterInferenceHTTPServer(srv, service.NewInferenceService(nil))
	request := httptest.NewRequest(http.MethodPut, "/api/v1/inference/services/id/policy", strings.NewReader(`{"policy":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Ani-Tenant-Id", "forged")
	response := httptest.NewRecorder()
	srv.ServeHTTP(response, request)
	require.Equal(t, 400, response.Code)
	request = httptest.NewRequest(http.MethodGet, "/api/v1/inference/services/id/policy", nil)
	request.Header.Set("X-Ani-Tenant-Id", "forged")
	response = httptest.NewRecorder()
	srv.ServeHTTP(response, request)
	require.Equal(t, 400, response.Code)
}
