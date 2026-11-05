package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/middleware"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// Every dataset route runs the server middleware chain first. This test proves
// two things at once for all seventeen public paths: the route is actually
// registered, and a denied caller never reaches the body parser or the owner.
// A successful dataset response is not substituted here; it needs the owner.
func TestModelDevDatasetRoutesEnterAuthentication(t *testing.T) {
	const datasetID = "11111111-2222-4333-8444-555555555555"
	cases := []struct {
		method, path, body string
	}{
		{"POST", "/api/v1/modeldev/datasets", `{"idempotency_key":"k1","data":{"name":"set"}}`},
		{"GET", "/api/v1/modeldev/datasets", ""},
		{"GET", "/api/v1/modeldev/datasets/" + datasetID, ""},
		{"PATCH", "/api/v1/modeldev/datasets/" + datasetID, `{"idempotency_key":"k2","update_mask":"name","data":{"name":"s2","expected_revision":"1"}}`},
		{"POST", "/api/v1/modeldev/datasets/" + datasetID + ":archive", `{"idempotency_key":"k3","data":{"expected_revision":"1"}}`},
		{"POST", "/api/v1/modeldev/datasets/" + datasetID + ":restore", `{"idempotency_key":"k4","data":{"expected_revision":"1"}}`},
		{"POST", "/api/v1/modeldev/dataset-sources:inspect", `{"data":{"mode":"LIST_FILES","source":{"provider":"HUGGINGFACE","repo_id":"a/b","revision":"main"}}}`},
		{"POST", "/api/v1/modeldev/datasets/" + datasetID + ":import", `{"idempotency_key":"k5","data":{"selection_fingerprint":"` + strings.Repeat("a", 64) + `"}}`},
		{"GET", "/api/v1/modeldev/datasets/" + datasetID + "/versions", ""},
		{"GET", "/api/v1/modeldev/input-versions/" + datasetID + "/preview", ""},
		{"GET", "/api/v1/modeldev/datasets/" + datasetID + "/import-tasks", ""},
		{"GET", "/api/v1/modeldev/dataset-import-tasks/" + datasetID, ""},
		{"POST", "/api/v1/modeldev/dataset-import-tasks/" + datasetID + ":cancel", `{"idempotency_key":"k6"}`},
		{"POST", "/api/v1/modeldev/dataset-import-tasks/" + datasetID + ":retry", `{"idempotency_key":"k7"}`},
		{"GET", "/api/v1/modeldev/datasets/" + datasetID + "/operations", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			entered := 0
			server := khttp.NewServer(khttp.Middleware(func(next middleware.Handler) middleware.Handler {
				return func(context.Context, interface{}) (interface{}, error) {
					entered++
					return nil, errors.Unauthorized("INVALID_LOGIN", "user login required")
				}
			}))
			registerModelDevHTTP(server, nil)
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			out := httptest.NewRecorder()
			server.ServeHTTP(out, r)
			if out.Code == http.StatusNotFound {
				t.Fatalf("%s %s is not registered (404)", tc.method, tc.path)
			}
			if out.Code != http.StatusUnauthorized || entered != 1 {
				t.Fatalf("status=%d auth_calls=%d, want 401 with exactly one authentication check", out.Code, entered)
			}
		})
	}
}

// The strict adapter accepts exactly the documented body, and rejects the
// shapes a permissive binder would swallow. Each case stops before the service
// call, so a nil ModelDevService is enough.
func TestModelDevDatasetBodyIsStrict(t *testing.T) {
	const id = "11111111-2222-4333-8444-555555555555"
	cases := []struct {
		name, method, path, body, contentType string
		want                                  int
	}{
		{"unknown top-level key", "POST", "/api/v1/modeldev/datasets", `{"idempotency_key":"k1","data":{"name":"s"},"tenant_id":"foreign"}`, "application/json", http.StatusBadRequest},
		{"duplicate key", "POST", "/api/v1/modeldev/datasets", `{"idempotency_key":"k1","idempotency_key":"k2","data":{"name":"s"}}`, "application/json", http.StatusBadRequest},
		{"missing idempotency key", "POST", "/api/v1/modeldev/datasets", `{"data":{"name":"s"}}`, "application/json", http.StatusBadRequest},
		{"unknown nested field", "POST", "/api/v1/modeldev/datasets", `{"idempotency_key":"k1","data":{"name":"s","owner":"x"}}`, "application/json", http.StatusBadRequest},
		{"trailing document", "POST", "/api/v1/modeldev/datasets", `{"idempotency_key":"k1","data":{"name":"s"}}{}`, "application/json", http.StatusBadRequest},
		{"wrong content type", "POST", "/api/v1/modeldev/datasets", `{"idempotency_key":"k1","data":{"name":"s"}}`, "text/plain", http.StatusBadRequest},
		{"uppercase uuid path", "GET", "/api/v1/modeldev/datasets/11111111-2222-4333-8444-55555555555A", "", "application/json", http.StatusBadRequest},
		{"not a uuid path", "GET", "/api/v1/modeldev/datasets/not-a-uuid", "", "application/json", http.StatusBadRequest},
		{"unknown list filter", "GET", "/api/v1/modeldev/datasets?owner=me", "", "application/json", http.StatusBadRequest},
		{"bad page size", "GET", "/api/v1/modeldev/datasets?pageSize=101", "", "application/json", http.StatusBadRequest},
		{"body on a read", "GET", "/api/v1/modeldev/datasets/" + id, "{}", "application/json", http.StatusBadRequest},
		{"archive without data", "POST", "/api/v1/modeldev/datasets/" + id + ":archive", `{"idempotency_key":"k3"}`, "application/json", http.StatusBadRequest},
		{"cancel without key", "POST", "/api/v1/modeldev/dataset-import-tasks/" + id + ":cancel", `{}`, "application/json", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := khttp.NewServer()
			registerModelDevHTTP(server, nil)
			r := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			r.Header.Set("Content-Type", tc.contentType)
			out := httptest.NewRecorder()
			server.ServeHTTP(out, r)
			if out.Code != tc.want {
				t.Fatalf("status=%d body=%s, want %d", out.Code, out.Body.String(), tc.want)
			}
		})
	}
}
