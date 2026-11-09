package server

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	catalogv1 "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/pkg/middleware/auth"
	"go-wind-admin/pkg/middleware/requestid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const clusterOpsBodyLimit int64 = 32 << 20

var clusterOpsDecimal = regexp.MustCompile("^(0|[1-9][0-9]*)$")

func invalidClusterOpsRequest() error {
	return kerrors.BadRequest("INVALID_CLUSTEROPS_REQUEST", "invalid cluster operations request")
}

// This filter validates the transport contract only; generated HTTP handlers
// still bind and execute all ten unary methods through the normal middleware.
func clusterOpsHTTPFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL == nil || !(r.URL.Path == auth.ClusterOpsHTTPPrefix || strings.HasPrefix(r.URL.Path, auth.ClusterOpsHTTPPrefix+"/")) {
			next.ServeHTTP(w, r)
			return
		}
		for _, rawPart := range strings.Split(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/") {
			part, err := url.PathUnescape(rawPart)
			if err != nil || !clusterOpsValidSegment(part) {
				writeClusterOpsHTTPError(w, r, invalidClusterOpsRequest())
				return
			}
		}
		operation, _, matched := auth.MatchClusterOpsHTTP(r.Method, r.URL.Path)
		if !matched {
			// Unknown operations stay unknown; malformed targets cannot be normalized
			// into a different Kubernetes resource by a router redirect.
			for _, part := range strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/") {
				if !clusterOpsValidSegment(part) {
					writeClusterOpsHTTPError(w, r, invalidClusterOpsRequest())
					return
				}
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.RawPath != "" {
			decoded, err := url.PathUnescape(r.URL.RawPath)
			if err != nil || decoded != r.URL.Path {
				writeClusterOpsHTTPError(w, r, invalidClusterOpsRequest())
				return
			}
		}
		for header := range r.Header {
			lower := strings.ToLower(header)
			if strings.HasPrefix(lower, "x-ani-") || lower == "x-tenant-id" || lower == "x-resource-tenant-id" {
				writeClusterOpsHTTPError(w, r, invalidClusterOpsRequest())
				return
			}
		}
		if err := validateClusterOpsQuery(r, operation); err != nil {
			writeClusterOpsHTTPError(w, r, err)
			return
		}
		if clusterOpsHasWriteBody(operation) {
			raw, err := decodeClusterOpsBody(r, operation)
			if err != nil {
				writeClusterOpsHTTPError(w, r, err)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			r.ContentLength = int64(len(raw))
		} else if r.Body != nil {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err != nil || len(raw) != 0 {
				writeClusterOpsHTTPError(w, r, invalidClusterOpsRequest())
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func clusterOpsValidSegment(value string) bool {
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

func clusterOpsMethod(operation string) string {
	return strings.TrimPrefix(operation, auth.ClusterOpsOperationPrefix)
}
func clusterOpsHasWriteBody(operation string) bool {
	switch clusterOpsMethod(operation) {
	case "CreateResource", "UpdateResource", "ScaleWorkload", "RestartWorkload":
		return true
	}
	return false
}

func clusterOpsQueryFields(operation string) map[string]bool {
	allowed := map[string]bool{}
	var names []string
	switch clusterOpsMethod(operation) {
	case "ListResources":
		names = []string{"group", "version", "resource", "namespace", "all_namespaces", "page_size", "continue_token", "name", "label_selector", "field_selector"}
	case "GetResource":
		names = []string{"group", "version", "resource", "namespace", "include_secret_data"}
	case "DeleteResource":
		names = []string{"group", "version", "resource", "namespace", "expected_uid"}
	case "StreamPodLogs":
		names = []string{"container", "expected_pod_uid", "follow", "previous", "timestamps", "tail_lines", "since_seconds", "since_time", "limit_bytes"}
	}
	for _, name := range names {
		allowed[name] = true
	}
	return allowed
}

func validateClusterOpsQuery(r *http.Request, operation string) error {
	if r == nil || r.URL == nil || r.URL.ForceQuery {
		return invalidClusterOpsRequest()
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return invalidClusterOpsRequest()
	}
	allowed := clusterOpsQueryFields(operation)
	for key, items := range values {
		if !allowed[key] || len(items) != 1 {
			return invalidClusterOpsRequest()
		}
		value := items[0]
		switch key {
		case "all_namespaces", "include_secret_data", "follow", "previous", "timestamps":
			if value != "true" && value != "false" {
				return invalidClusterOpsRequest()
			}
		case "page_size", "tail_lines", "since_seconds", "limit_bytes":
			if !clusterOpsDecimal.MatchString(value) {
				return invalidClusterOpsRequest()
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return invalidClusterOpsRequest()
			}
			if key == "page_size" && (n < 1 || n > 200) || (key == "since_seconds" || key == "limit_bytes") && n == 0 {
				return invalidClusterOpsRequest()
			}
		case "since_time":
			if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
				return invalidClusterOpsRequest()
			}
		}
	}
	switch clusterOpsMethod(operation) {
	case "ListResources", "GetResource", "DeleteResource":
		if values.Get("version") == "" || values.Get("resource") == "" {
			return invalidClusterOpsRequest()
		}
	}
	if clusterOpsMethod(operation) == "DeleteResource" && values.Get("expected_uid") == "" {
		return invalidClusterOpsRequest()
	}
	if _, ok := values["since_seconds"]; ok {
		if _, also := values["since_time"]; also {
			return invalidClusterOpsRequest()
		}
	}
	if clusterOpsMethod(operation) == "StreamPodLogs" && len(r.Header.Values("Last-Event-ID")) > 0 {
		return invalidClusterOpsRequest()
	}
	return nil
}

func clusterOpsRequestMessage(operation string) proto.Message {
	switch clusterOpsMethod(operation) {
	case "ListClusters":
		return &catalogv1.ClusterOpsListClustersRequest{}
	case "GetClusterOverview":
		return &catalogv1.ClusterOpsGetClusterOverviewRequest{}
	case "ListResourceTypes":
		return &catalogv1.ClusterOpsListResourceTypesRequest{}
	case "ListResources":
		return &catalogv1.ClusterOpsListResourcesRequest{}
	case "GetResource":
		return &catalogv1.ClusterOpsGetResourceRequest{}
	case "CreateResource":
		return &catalogv1.ClusterOpsCreateResourceRequest{}
	case "UpdateResource":
		return &catalogv1.ClusterOpsUpdateResourceRequest{}
	case "DeleteResource":
		return &catalogv1.ClusterOpsDeleteResourceRequest{}
	case "ScaleWorkload":
		return &catalogv1.ClusterOpsScaleWorkloadRequest{}
	case "RestartWorkload":
		return &catalogv1.ClusterOpsRestartWorkloadRequest{}
	case "StreamPodLogs":
		return &catalogv1.ClusterOpsStreamPodLogsRequest{}
	}
	return nil
}

func decodeClusterOpsBody(r *http.Request, operation string) ([]byte, error) {
	if r == nil || r.Body == nil || r.URL == nil || r.URL.RawQuery != "" || r.URL.ForceQuery {
		return nil, invalidClusterOpsRequest()
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, invalidClusterOpsRequest()
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, clusterOpsBodyLimit+1))
	if err != nil {
		return nil, invalidClusterOpsRequest()
	}
	if int64(len(raw)) > clusterOpsBodyLimit {
		return nil, kerrors.New(http.StatusRequestEntityTooLarge, "CLUSTEROPS_REQUEST_TOO_LARGE", "cluster operations request exceeds the transport limit")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || len(object) != 1 || object["data"] == nil || bytes.Equal(bytes.TrimSpace(object["data"]), []byte("null")) {
		return nil, invalidClusterOpsRequest()
	}
	message := clusterOpsRequestMessage(operation)
	if message == nil {
		return nil, invalidClusterOpsRequest()
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, message); err != nil {
		return nil, invalidClusterOpsRequest()
	}
	value := message.ProtoReflect()
	dataField := value.Descriptor().Fields().ByName("data")
	if dataField == nil || !value.Has(dataField) {
		return nil, invalidClusterOpsRequest()
	}
	data := value.Get(dataField).Message()
	required := []string{}
	switch clusterOpsMethod(operation) {
	case "CreateResource":
		required = []string{"name", "manifest_yaml"}
	case "UpdateResource":
		required = []string{"manifest_yaml", "expected_uid", "expected_resource_version"}
	case "ScaleWorkload", "RestartWorkload":
		required = []string{"expected_uid", "expected_resource_version"}
	default:
		return nil, invalidClusterOpsRequest()
	}
	for _, name := range required {
		field := data.Descriptor().Fields().ByName(protoreflect.Name(name))
		if field == nil || data.Get(field).String() == "" {
			return nil, invalidClusterOpsRequest()
		}
	}
	typeField := data.Descriptor().Fields().ByName("type")
	if typeField == nil || !data.Has(typeField) {
		return nil, invalidClusterOpsRequest()
	}
	resourceType := data.Get(typeField).Message()
	for _, name := range []string{"version", "resource"} {
		field := resourceType.Descriptor().Fields().ByName(protoreflect.Name(name))
		if resourceType.Get(field).String() == "" {
			return nil, invalidClusterOpsRequest()
		}
	}
	if clusterOpsMethod(operation) == "ScaleWorkload" {
		var rawData map[string]json.RawMessage
		if json.Unmarshal(object["data"], &rawData) != nil || !clusterOpsDecimal.Match(rawData["replicas"]) {
			return nil, invalidClusterOpsRequest()
		}
		field := data.Descriptor().Fields().ByName("replicas")
		if !data.Has(field) || data.Get(field).Int() < 0 {
			return nil, invalidClusterOpsRequest()
		}
	}
	return raw, nil
}

func writeClusterOpsHTTPError(w http.ResponseWriter, r *http.Request, err error) {
	known := kerrors.FromError(err)
	metadata := map[string]string{}
	for key, value := range known.Metadata {
		metadata[key] = value
	}
	id, ok := requestid.FromContext(r.Context())
	if !ok {
		id = r.Header.Get(requestid.HeaderRequestID)
	}
	if id != "" {
		metadata["request_id"] = id
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(int(known.Code))
	_ = json.NewEncoder(w).Encode(struct {
		Code     int32             `json:"code"`
		Reason   string            `json:"reason"`
		Message  string            `json:"message"`
		Metadata map[string]string `json:"metadata"`
	}{known.Code, known.Reason, known.Message, metadata})
}
