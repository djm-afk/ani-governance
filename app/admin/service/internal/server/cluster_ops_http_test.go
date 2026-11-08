package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/middleware"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	clusteropsv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/clusterops/v1"
	adminv1 "go-wind-admin/api/gen/go/admin/service/v1"
	"go-wind-admin/app/admin/service/internal/service"
	"go-wind-admin/pkg/middleware/auth"
)

const clusterOpsTestResources = "/api/v1/admin/cluster-ops/clusters/configured/resources"

func TestClusterOpsHTTPPreservesRawBodyAndEncodedIdentity(t *testing.T) {
	manifest := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: test\ndata:\n  integer: '9007199254740993'\n  text: " + strings.Repeat("x", 70<<10) + "\n"
	raw, err := json.Marshal(map[string]any{"data": map[string]any{
		"type":      map[string]string{"group": "", "version": "v1", "resource": "configmaps"},
		"namespace": "test-space", "name": "test", "manifest_yaml": manifest, "dry_run": true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", clusterOpsTestResources, strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	called := false
	clusterOpsHTTPFilter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		forwarded, err := io.ReadAll(r.Body)
		if err != nil || string(forwarded) != string(raw) || r.ContentLength != int64(len(raw)) {
			t.Errorf("raw body was changed or truncated: length=%d err=%v", len(forwarded), err)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(httptest.NewRecorder(), request)
	if !called {
		t.Fatal("legal YAML larger than 64 KiB was rejected before the generated handler")
	}

	query := url.Values{"group": {"rbac.authorization.k8s.io"}, "version": {"v1"}, "resource": {"clusterroles"}}
	for _, tc := range []struct {
		segment, decoded string
		want             int
	}{
		{"system%3Areader", "system:reader", http.StatusNoContent},
		{"literal%252Fname", "literal%2Fname", http.StatusNoContent},
		{"bad%2Fsegment", "", http.StatusBadRequest},
		{"%2E%2E", "", http.StatusBadRequest},
		{"bad%00name", "", http.StatusBadRequest},
	} {
		t.Run(tc.segment, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			clusterOpsHTTPFilter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/"+tc.decoded) {
					t.Errorf("identity decoded again: %q", r.URL.Path)
				}
				w.WriteHeader(http.StatusNoContent)
			})).ServeHTTP(recorder, httptest.NewRequest("GET", clusterOpsTestResources+"/"+tc.segment+"?"+query.Encode(), nil))
			if recorder.Code != tc.want {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestClusterOpsHTTPRejectsAmbiguousOrForgedInput(t *testing.T) {
	scale := `{"data":{"type":{"group":"apps","version":"v1","resource":"deployments"},"namespace":"test-space","expected_uid":"uid","expected_resource_version":"rv","replicas":0}}`
	create := `{"data":{"type":{"version":"v1","resource":"configmaps"},"namespace":"test-space","name":"test","manifest_yaml":"opaque text passed to Res"}}`
	list := clusterOpsTestResources + "?version=v1&resource=pods&all_namespaces=true"
	logs := "/api/v1/admin/cluster-ops/clusters/configured/namespaces/test-space/pods/test/logs"
	for _, tc := range []struct {
		name, method, path, body, header, value string
	}{
		{"unknown query", "GET", list + "&tenant_id=0", "", "", ""},
		{"duplicate query", "GET", list + "&resource=secrets", "", "", ""},
		{"missing GVR", "GET", clusterOpsTestResources + "?version=v1", "", "", ""},
		{"boolean spelling", "GET", clusterOpsTestResources + "?version=v1&resource=pods&all_namespaces=True", "", "", ""},
		{"empty boolean", "GET", clusterOpsTestResources + "?version=v1&resource=pods&all_namespaces=", "", "", ""},
		{"zero page", "GET", list + "&page_size=0", "", "", ""},
		{"oversized page", "GET", list + "&page_size=201", "", "", ""},
		{"noncanonical page", "GET", list + "&page_size=01", "", "", ""},
		{"query on overview", "GET", "/api/v1/admin/cluster-ops/clusters/configured/overview?group=", "", "", ""},
		{"empty query marker", "GET", "/api/v1/admin/cluster-ops/clusters?", "", "", ""},
		{"query body", "GET", list, `{}`, "", ""},
		{"forged actor", "GET", list, "", "X-Ani-Actor", "governance:user:1"},
		{"forged scope", "GET", list, "", "X-Ani-Scope", "platform"},
		{"forged tenant", "GET", list, "", "X-Tenant-ID", "0"},
		{"write query", "POST", clusterOpsTestResources + "?group=", create, "", ""},
		{"wrong media", "POST", clusterOpsTestResources, create, "Content-Type", "text/plain"},
		{"null body", "POST", clusterOpsTestResources, `null`, "", ""},
		{"array body", "POST", clusterOpsTestResources, `[]`, "", ""},
		{"null data", "POST", clusterOpsTestResources, `{"data":null}`, "", ""},
		{"second JSON object", "POST", clusterOpsTestResources, create + `{}`, "", ""},
		{"duplicate data", "POST", clusterOpsTestResources, strings.TrimSuffix(create, "}") + `,"data":{}}`, "", ""},
		{"outer path field", "POST", clusterOpsTestResources, strings.TrimSuffix(create, "}") + `,"cluster_id":"other"}`, "", ""},
		{"inner identity", "POST", clusterOpsTestResources, strings.Replace(create, `"name":"test"`, `"name":"test","actor":"user"`, 1), "", ""},
		{"scale path override", "POST", clusterOpsTestResources + "/test/scale", strings.Replace(scale, `"replicas":0`, `"replicas":0,"name":"other"`, 1), "", ""},
		{"missing replicas", "POST", clusterOpsTestResources + "/test/scale", strings.Replace(scale, `,"replicas":0`, "", 1), "", ""},
		{"null replicas", "POST", clusterOpsTestResources + "/test/scale", strings.Replace(scale, `"replicas":0`, `"replicas":null`, 1), "", ""},
		{"negative replicas", "POST", clusterOpsTestResources + "/test/scale", strings.Replace(scale, `"replicas":0`, `"replicas":-1`, 1), "", ""},
		{"decimal replicas", "POST", clusterOpsTestResources + "/test/scale", strings.Replace(scale, `"replicas":0`, `"replicas":1.0`, 1), "", ""},
		{"string replicas", "POST", clusterOpsTestResources + "/test/scale", strings.Replace(scale, `"replicas":0`, `"replicas":"0"`, 1), "", ""},
		{"replicas overflow", "POST", clusterOpsTestResources + "/test/scale", strings.Replace(scale, `"replicas":0`, `"replicas":2147483648`, 1), "", ""},
		{"delete missing UID", "DELETE", clusterOpsTestResources + "/test?version=v1&resource=configmaps", "", "", ""},
		{"delete body", "DELETE", clusterOpsTestResources + "/test?version=v1&resource=configmaps&expected_uid=uid", `{}`, "", ""},
		{"logs resume", "GET", logs, "", "Last-Event-ID", "1"},
		{"logs overflow", "GET", logs + "?tail_lines=9223372036854775808", "", "", ""},
		{"logs exclusive since", "GET", logs + "?since_seconds=1&since_time=2026-11-08T00%3A00%3A00Z", "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.method == "POST" || tc.method == "PUT" {
				request.Header.Set("Content-Type", "application/json")
			}
			if tc.header != "" {
				request.Header.Set(tc.header, tc.value)
			}
			recorder := httptest.NewRecorder()
			clusterOpsHTTPFilter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("invalid input reached the generated handler")
			})).ServeHTTP(recorder, request)
			var body clusterOpsErrorBody
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || recorder.Code != 400 || body.Reason != "INVALID_CLUSTEROPS_REQUEST" {
				t.Fatalf("status=%d error=%v body=%s", recorder.Code, err, recorder.Body.String())
			}
		})
	}
	request := httptest.NewRequest("POST", clusterOpsTestResources+"/test/scale", strings.NewReader(scale))
	request.Header.Set("Content-Type", "application/json")
	called := false
	clusterOpsHTTPFilter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(httptest.NewRecorder(), request)
	if !called {
		t.Fatal("explicit replicas=0 must be accepted")
	}
}

type clusterOpsWhitespaceReader struct{ read int64 }

func (r *clusterOpsWhitespaceReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	r.read += int64(len(p))
	return len(p), nil
}

func TestClusterOpsHTTPBoundsTransportInputBeforeForwarding(t *testing.T) {
	source := &clusterOpsWhitespaceReader{}
	request := httptest.NewRequest("POST", clusterOpsTestResources, nil)
	request.Body = io.NopCloser(source)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	clusterOpsHTTPFilter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("oversized request was forwarded")
	})).ServeHTTP(recorder, request)
	var body clusterOpsErrorBody
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || recorder.Code != 413 || body.Reason != "CLUSTEROPS_REQUEST_TOO_LARGE" {
		t.Fatalf("status=%d error=%v body=%s", recorder.Code, err, recorder.Body.String())
	}
	if source.read != (32<<20)+1 {
		t.Fatalf("read %d input bytes, want 32 MiB plus one detection byte", source.read)
	}
}

func TestClusterOpsBudgetsAreExactAndPreserveCaller(t *testing.T) {
	for operation, policy := range auth.ClusterOpsOperations {
		path := strings.NewReplacer("{cluster_id}", "configured", "{namespace}", "test-space", "{pod_name}", "test", "{name}", "system:reader").Replace(policy.Path)
		t.Run(operation, func(t *testing.T) {
			instanceHTTPTimeoutFilter(time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				deadline, exists := r.Context().Deadline()
				if operation == clusterOpsLogsOperation {
					if exists {
						t.Fatal("ordinary deadline was applied to the bounded log stream")
					}
				} else if !exists || time.Until(deadline) < 34*time.Second || time.Until(deadline) > 35*time.Second {
					t.Fatalf("new unary HTTP budget=%v, exists=%v", time.Until(deadline), exists)
				}
			})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(policy.Method, path, nil))
			parent, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			instanceHTTPTimeoutFilter(time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				actual, _ := r.Context().Deadline()
				want, _ := parent.Deadline()
				if !actual.Equal(want) {
					t.Fatal("caller deadline was extended")
				}
				cancel()
				if r.Context().Err() != context.Canceled {
					t.Fatal("caller cancellation was lost")
				}
			})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(policy.Method, path, nil).WithContext(parent))
		})
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/users"},
		{"PATCH", clusterOpsTestResources + "/test"},
		{"GET", clusterOpsTestResources + "/test/extra"},
		{"POST", "/api/v1/admin/cluster-ops/clusters/configured/namespaces/test-space/pods/test/logs"},
	} {
		instanceHTTPTimeoutFilter(time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			deadline, exists := r.Context().Deadline()
			if !exists || time.Until(deadline) > time.Second || time.Until(deadline) < 500*time.Millisecond {
				t.Fatalf("unrelated route budget changed: %s %s", tc.method, tc.path)
			}
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))
	}
}

type clusterOpsResourceClientStub struct {
	service.ClusterOpsPlatformClient
	get func(context.Context, string, *clusteropsv1.GetResourceRequest) (*clusteropsv1.GetResourceResponse, error)
}

func (c *clusterOpsResourceClientStub) GetResource(ctx context.Context, actor string, req *clusteropsv1.GetResourceRequest) (*clusteropsv1.GetResourceResponse, error) {
	return c.get(ctx, actor, req)
}

func TestClusterOpsGeneratedHandlerKeepsEncodedNameAndRawFields(t *testing.T) {
	const objectJSON = `{"spec":{"exact":9007199254740993}}`
	const manifest = "spec:\n  exact: 9007199254740993\n"
	targets := make(chan string, 2)
	client := &clusterOpsResourceClientStub{get: func(_ context.Context, actor string, req *clusteropsv1.GetResourceRequest) (*clusteropsv1.GetResourceResponse, error) {
		if actor != "governance:user:9" || req.Ref == nil || req.Ref.Type == nil || req.Ref.Type.Group != "rbac.authorization.k8s.io" || req.Ref.Type.Resource != "clusterroles" {
			return nil, errors.New("generated handler lost the trusted actor or resource type")
		}
		targets <- req.Ref.Name
		return &clusteropsv1.GetResourceResponse{Resource: &clusteropsv1.ResourceDocument{
			Ref: req.Ref, Uid: "uid", ResourceVersion: "9007199254740993", ObjectJson: objectJSON,
			ReadOnly: true, ReadOnlyReason: "TENANT_NAMESPACE_READ_ONLY",
		}, ManifestYaml: manifest}, nil
	}}
	principal := clusterOpsLogPrincipal()
	principal.Claims.Permissions = []string{"cluster:ops:read"}
	server := khttp.NewServer(khttp.Timeout(0), khttp.Filter(clusterOpsHTTPFilter), khttp.ErrorEncoder(writeClusterOpsHTTPError),
		khttp.Middleware(func(next middleware.Handler) middleware.Handler {
			return func(ctx context.Context, req any) (any, error) {
				return next(auth.NewPrincipalContext(ctx, principal), req)
			}
		}))
	adminv1.RegisterPlatformClusterOpsServiceHTTPServer(server, service.NewPlatformClusterOpsService(client))
	actual := httptest.NewServer(server)
	defer actual.Close()
	actual.Client().Timeout = 2 * time.Second
	for _, tc := range []struct{ encoded, name string }{{"system%3Areader", "system:reader"}, {"literal%252Fname", "literal%2Fname"}} {
		response, err := actual.Client().Get(actual.URL + clusterOpsTestResources + "/" + tc.encoded + "?group=rbac.authorization.k8s.io&version=v1&resource=clusterroles")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		var body struct {
			Resource struct {
				UID             string `json:"uid"`
				ResourceVersion string `json:"resource_version"`
				ObjectJSON      string `json:"object_json"`
				ReadOnly        bool   `json:"read_only"`
				Reason          string `json:"read_only_reason"`
			} `json:"resource"`
			Manifest string `json:"manifest_yaml"`
		}
		if err != nil || json.Unmarshal(raw, &body) != nil || response.StatusCode != 200 || body.Resource.UID != "uid" || body.Resource.ResourceVersion != "9007199254740993" || body.Resource.ObjectJSON != objectJSON || body.Manifest != manifest || !body.Resource.ReadOnly || body.Resource.Reason != "TENANT_NAMESPACE_READ_ONLY" {
			t.Fatalf("generated HTTP mapping changed raw fields: status=%d err=%v body=%s", response.StatusCode, err, raw)
		}
		if name := clusterOpsWaitValue(t, targets); name != tc.name {
			t.Fatalf("generated handler decoded target twice: %q, want %q", name, tc.name)
		}
	}
}
