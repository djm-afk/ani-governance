package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlatformNetworkHTTPContract(t *testing.T) {
	const base = "/api/v1/admin/networks"
	for _, tc := range []struct {
		name, method, path, body string
		want                     int
	}{
		{"list", "GET", base + "/public-address-pools?limit=1&state=available", "", 204},
		{"default_limit", "GET", base + "/intranet-address-pools", "", 204},
		{"inventory", "GET", base + "/node-interfaces?node_name=node-a", "", 204},
		{"create_data", "POST", base + "/egress-gateways", `{"data":{"name":"gw","idempotency_key":"create"}}`, 204},
		{"legacy_window", "GET", base + "/vlan-networks?page=1", "", 400},
		{"zero", "GET", base + "/vlan-networks?limit=0", "", 400},
		{"overflow", "GET", base + "/vlan-networks?limit=101", "", 400},
		{"noncanonical", "GET", base + "/vlan-networks?limit=01", "", 400},
		{"duplicate", "GET", base + "/vlan-networks?limit=1&limit=2", "", 400},
		{"inventory_window", "GET", base + "/node-interfaces?limit=1", "", 400},
		{"unknown_filter", "GET", base + "/public-address-pools?tenant_id=0", "", 400},
		{"identity_in_body", "POST", base + "/egress-gateways", `{"data":{"name":"gw","actor":"governance:user:1"}}`, 400},
		{"bad_reference", "POST", base + "/vlan-networks", `{"data":{"name":"vlan","device_id":"vpc_0123456789abcdef0123456789abcdef"}}`, 400},
		{"duplicate_body_field", "POST", base + "/egress-gateways", `{"data":{"name":"a","name":"b"}}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			NetworkHTTPFilter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				op, _, _, _ := networkHTTPRoute(r)
				if !IsPlatformNetworkOperation(op) || ValidateNetworkRequest(r, op) != nil {
					t.Fatal("validated platform operation missing")
				}
				if allowsAPIKey(op) {
					t.Fatal("tenant key gained platform authority")
				}
				w.WriteHeader(204)
			})).ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
	for _, header := range []string{"X-Ani-Scope", "X-Ani-Tenant-Id", "X-Ani-Actor", "X-Platform-Admin"} {
		t.Run(header, func(t *testing.T) {
			r := httptest.NewRequest("GET", base+"/capabilities", nil)
			r.Header.Set(header, "platform")
			w := httptest.NewRecorder()
			NetworkHTTPFilter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("spoofed identity reached decoding") })).ServeHTTP(w, r)
			if w.Code != 400 {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}
