package auth

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const ClusterOpsOperationPrefix = "/admin.service.v1.PlatformClusterOpsService/"
const ClusterOpsHTTPPrefix = "/api/v1/admin/cluster-ops"

// ClusterOpsOperationPolicy describes an API action, never resource ownership.
type ClusterOpsOperationPolicy struct {
	Method     string
	Path       string
	Permission string
	ReadOnly   bool
}

var ClusterOpsOperations = map[string]ClusterOpsOperationPolicy{
	ClusterOpsOperationPrefix + "ListClusters":       {"GET", ClusterOpsHTTPPrefix + "/clusters", "cluster:ops:read", true},
	ClusterOpsOperationPrefix + "GetClusterOverview": {"GET", ClusterOpsHTTPPrefix + "/clusters/{cluster_id}/overview", "cluster:ops:read", true},
	ClusterOpsOperationPrefix + "ListResourceTypes":  {"GET", ClusterOpsHTTPPrefix + "/clusters/{cluster_id}/resource-types", "cluster:ops:read", true},
	ClusterOpsOperationPrefix + "ListResources":      {"GET", ClusterOpsHTTPPrefix + "/clusters/{cluster_id}/resources", "cluster:ops:read", true},
	ClusterOpsOperationPrefix + "GetResource":        {"GET", ClusterOpsHTTPPrefix + "/clusters/{cluster_id}/resources/{name}", "cluster:ops:read", true},
	ClusterOpsOperationPrefix + "CreateResource":     {"POST", ClusterOpsHTTPPrefix + "/clusters/{cluster_id}/resources", "cluster:ops:create", false},
	ClusterOpsOperationPrefix + "UpdateResource":     {"PUT", ClusterOpsHTTPPrefix + "/clusters/{cluster_id}/resources/{name}", "cluster:ops:update", false},
	ClusterOpsOperationPrefix + "DeleteResource":     {"DELETE", ClusterOpsHTTPPrefix + "/clusters/{cluster_id}/resources/{name}", "cluster:ops:delete", false},
	ClusterOpsOperationPrefix + "ScaleWorkload":      {"POST", ClusterOpsHTTPPrefix + "/clusters/{cluster_id}/resources/{name}/scale", "cluster:ops:scale", false},
	ClusterOpsOperationPrefix + "RestartWorkload":    {"POST", ClusterOpsHTTPPrefix + "/clusters/{cluster_id}/resources/{name}/restart", "cluster:ops:restart", false},
	ClusterOpsOperationPrefix + "StreamPodLogs":      {"GET", ClusterOpsHTTPPrefix + "/clusters/{cluster_id}/namespaces/{namespace}/pods/{pod_name}/logs", "cluster:ops:logs", true},
}

func IsClusterOpsOperation(operation string) bool { _, ok := ClusterOpsPolicy(operation); return ok }
func ClusterOpsPolicy(operation string) (ClusterOpsOperationPolicy, bool) {
	p, ok := ClusterOpsOperations[operation]
	return p, ok
}

// MatchClusterOpsHTTP matches every path segment, including all resource targets.
// decodedPath is already decoded by net/http; never unescape it a second time.
func MatchClusterOpsHTTP(method, decodedPath string) (string, ClusterOpsOperationPolicy, bool) {
	parts := strings.Split(decodedPath, "/")
	for operation, policy := range ClusterOpsOperations {
		if policy.Method != method {
			continue
		}
		template := strings.Split(policy.Path, "/")
		if len(parts) != len(template) {
			continue
		}
		match := true
		for i, segment := range template {
			if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
				if !validClusterOpsPathSegment(parts[i]) {
					match = false
					break
				}
			} else if segment != parts[i] {
				match = false
				break
			}
		}
		if match {
			return operation, policy, true
		}
	}
	return "", ClusterOpsOperationPolicy{}, false
}

func validClusterOpsPathSegment(value string) bool {
	if !utf8.ValidString(value) || value == "" || value == "." || value == ".." || strings.Contains(value, "/") {
		return false
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
