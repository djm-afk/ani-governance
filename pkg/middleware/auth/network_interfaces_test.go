package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestOwnerNetworkInterfaceExactOperationsAcceptSignedRequests(t *testing.T) {
	const id = "33333333-3333-4333-8333-333333333333"
	now := time.Unix(1700000000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	for operation, policy := range OwnerNetworkInterfaceOperations {
		t.Run(operation, func(t *testing.T) {
			path := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(policy.Path, "{vm_id}", id), "{instance_id}", id), "{slot}", "primary")
			body := ""
			if policy.Method == "PUT" {
				body = `{"data":{"mode":"manual","vnic_id":"vnic_0123456789abcdef0123456789abcdef","expected_version":"9","idempotency_key":"33333333-3333-4333-8333-333333333333"}}`
			}
			r := httptest.NewRequest(policy.Method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-Access-Key", vectorAK)
			r.Header.Set("X-Timestamp", ts)
			r.Header.Set("X-Signature", NetworkSignature(vectorSK, policy.Method, path, "", vectorAK, ts, []byte(body)))
			store := &signingStoreStub{key: &SigningKey{ID: 9, TenantID: 5, Role: "tenant:network-admin", RoleAllowed: true, Secret: vectorSK}}
			called := false
			w := httptest.NewRecorder()
			NetworkHTTPFilter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				p, e := verifySignature(context.Background(), r, operation, store, now)
				if e != nil || p == nil || p.Type != SubjectAPIKey || p.ID != 9 {
					t.Fatal("new owner request rejected", p, e)
				}
				raw, e := io.ReadAll(r.Body)
				if e != nil || string(raw) != body {
					t.Fatal("signed body lost")
				}
				w.WriteHeader(204)
			})).ServeHTTP(w, r)
			if !called || w.Code != 204 {
				t.Fatal("strict owner network route rejected", w.Code, w.Body.String())
			}
		})
	}
	if allowsAPIKey("/admin.service.v1.VMService/RestartVM") || allowsAPIKey("/admin.service.v1.InstanceService/StartInstance") {
		t.Fatal("unrelated owner API key surface expanded")
	}
}
func TestVNicAndOwnerNetworkQueryRejectAmbiguity(t *testing.T) {
	for _, target := range []string{
		"/api/v1/networks/vnics?page=01",
		"/api/v1/networks/vnics?pageSize=101",
		"/api/v1/networks/vnics?page=1&page=2",
		"/api/v1/networks/vnics?limit=2",
		"/api/v1/networks/vnics?binding_state=ready",
		"/api/v1/networks/vnics?bindable_to_kind=sandbox",
		"/api/v1/virtual-machines/33333333-3333-4333-8333-333333333333/network-interfaces?tenant_id=forged",
	} {
		w := httptest.NewRecorder()
		NetworkHTTPFilter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("ambiguous network request passed", target) })).ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 400 {
			t.Fatal("unexpected response", target, w.Code)
		}
	}
}
