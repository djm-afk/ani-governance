package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResourceUsageHTTPStrictQuery(t *testing.T) {
	base := "?start=2026-10-27T00%3A00%3A00Z&end=2026-10-28T00%3A00%3A00Z"
	for _, tc := range []struct {
		suffix      string
		list, valid bool
	}{
		{"", false, true}, {"&metric=cpu", false, true}, {"&page=3&pageSize=100&metric=memory&order=asc&keyword=Tenant", true, true},
		{"&page=4294967295", true, true}, {"&page=01", true, false}, {"&page=0", true, false}, {"&page=4294967296", true, false},
		{"&pageSize=101", true, false}, {"&pageSize=", true, false}, {"&page=1&page=2", true, false}, {"&page_size=2", true, false},
		{"&tenantId=2", true, false}, {"&metric=gpu&metric=cpu", true, false}, {"&metric=GPU", true, false}, {"&order=bad", true, false},
		{"&keyword=%20%20", true, false}, {"&page=2", false, false}, {"&clusterId=secret", false, false}, {"&actor=root", false, false},
	} {
		if err := validateResourceUsageHTTP(httptest.NewRequest("GET", resourceUsagePrefix+"overview"+base+tc.suffix, nil), tc.list); (err == nil) != tc.valid {
			t.Errorf("suffix=%s list=%v err=%v", tc.suffix, tc.list, err)
		}
	}
	for _, query := range []string{"", "?start=2026-10-27T00:00:00Z", "?start=2026-10-27T00:00:00.1Z&end=2026-10-28T00:00:00Z"} {
		if validateResourceUsageHTTP(httptest.NewRequest("GET", resourceUsagePrefix+"overview"+query, nil), false) == nil {
			t.Errorf("accepted %s", query)
		}
	}
	if validateResourceUsageHTTP(httptest.NewRequest("GET", resourceUsagePrefix+"overview"+base, strings.NewReader(`{}`)), false) == nil {
		t.Fatal("accepted GET body")
	}
}

func TestResourceUsageHTTPBudgetPreservesParentAndOrdinary(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		budget       time.Duration
	}{
		{"GET", resourceUsagePrefix + "overview", 45 * time.Second},
		{"GET", resourceUsagePrefix + "tenants", 45 * time.Second},
		{"GET", resourceUsagePrefix + "tenants/2", 45 * time.Second},
		{"POST", resourceUsagePrefix + "overview", 2 * time.Second},
		{"GET", "/api/v1/admin/tenants", 2 * time.Second},
	} {
		instanceHTTPTimeoutFilter(2*time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			d, ok := r.Context().Deadline()
			if !ok || time.Until(d) > tc.budget || time.Until(d) < tc.budget-time.Second {
				t.Fatalf("%s %s budget=%v", tc.method, tc.path, time.Until(d))
			}
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))
	}
	parent, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	instanceHTTPTimeoutFilter(2*time.Second)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		d, _ := r.Context().Deadline()
		if time.Until(d) > 100*time.Millisecond {
			t.Fatal("parent deadline lost")
		}
		cancel()
		if r.Context().Err() != context.Canceled {
			t.Fatal("parent cancellation lost")
		}
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", resourceUsagePrefix+"overview", nil).WithContext(parent))
}
