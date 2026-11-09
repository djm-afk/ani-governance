package auth

import (
	"strings"
	"testing"
)

func TestClusterOpsPoliciesMatchOnlyTheirExactMethodAndSegments(t *testing.T) {
	if len(ClusterOpsOperations) != 11 {
		t.Fatalf("operation count=%d, want eleven", len(ClusterOpsOperations))
	}
	permissions := map[string]int{}
	for operation, policy := range ClusterOpsOperations {
		path := strings.NewReplacer("{cluster_id}", "configured-cluster", "{namespace}", "test-space", "{pod_name}", "test-pod", "{name}", "system:reader").Replace(policy.Path)
		gotOperation, gotPolicy, ok := MatchClusterOpsHTTP(policy.Method, path)
		if !ok || gotOperation != operation || gotPolicy != policy {
			t.Fatalf("%s %s => %q %+v %v", policy.Method, path, gotOperation, gotPolicy, ok)
		}
		if !IsClusterOpsOperation(operation) {
			t.Fatalf("operation missing from exact policy: %s", operation)
		}
		permissions[policy.Permission]++
		if policy.ReadOnly != (strings.HasSuffix(operation, "/StreamPodLogs") || policy.Permission == "cluster:ops:read") {
			t.Fatalf("wrong API read-only classification: %s", operation)
		}
		for _, invalidPath := range []string{path + "/", path + "/extra", "/other" + path} {
			// A ListResources path plus one segment is the valid GetResource
			// route. It must never retain the shorter list operation.
			if invalidOperation, _, match := MatchClusterOpsHTTP(policy.Method, invalidPath); match && invalidOperation == operation {
				t.Fatalf("non-exact path retained operation %s: %s", operation, invalidPath)
			}
		}
	}
	if len(permissions) != 7 || permissions["cluster:ops:read"] != 5 || permissions["cluster:ops:logs"] != 1 {
		t.Fatalf("wrong capability grouping: %+v", permissions)
	}
	for _, unknown := range []string{ClusterOpsOperationPrefix, ClusterOpsOperationPrefix + "Unknown", "/other.Service/ListClusters"} {
		if IsClusterOpsOperation(unknown) {
			t.Fatalf("unknown operation accepted: %s", unknown)
		}
	}
	if _, _, ok := MatchClusterOpsHTTP("PATCH", ClusterOpsHTTPPrefix+"/clusters/c/resources/r"); ok {
		t.Fatal("unsupported method matched")
	}
}

func TestClusterOpsPoliciesDoNotNormalizeResourceIdentity(t *testing.T) {
	prefix := ClusterOpsHTTPPrefix + "/clusters/c/resources/"
	for _, name := range []string{"system:reader", "literal%2Fname", "with.dot"} {
		if _, _, ok := MatchClusterOpsHTTP("GET", prefix+name); !ok {
			t.Fatalf("legal opaque segment rejected: %q", name)
		}
	}
	for _, name := range []string{"", ".", "..", "a/b", "a\x00b", "a\x7fb", "a\u0085b", string([]byte{0xff})} {
		if _, _, ok := MatchClusterOpsHTTP("GET", prefix+name); ok {
			t.Fatalf("invalid target segment accepted: %q", name)
		}
	}
}
