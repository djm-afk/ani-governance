package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-kratos/kratos/v2/middleware"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/stretchr/testify/require"
	adminv1 "go-wind-admin/api/gen/go/admin/service/v1"
	modeldevv1 "go-wind-admin/api/gen/go/modeldev/service/v1"
	"google.golang.org/protobuf/proto"
)

// A dataset detail route returns the resource object itself, exactly like the
// VPC, image, input-version and execution detail routes. The frontend reads the
// fields at the top level, so an extra envelope is a contract break, not a style
// choice.
func TestDatasetDetailHTTPReturnsResourceObjects(t *testing.T) {
	const (
		datasetID = "11111111-2222-4333-8444-555555555555"
		taskID    = "22222222-3333-4444-8555-666666666666"
		versionID = "33333333-4444-4555-8666-777777777777"
	)
	tests := []struct {
		name, path, wrapper, key, value string
		response                        proto.Message
	}{
		{"Dataset", "/api/v1/modeldev/datasets/" + datasetID, "dataset", "dataset_id", datasetID,
			&modeldevv1.GetDatasetResponse{Dataset: &modeldevv1.DatasetView{DatasetId: datasetID}}},
		{"ImportTask", "/api/v1/modeldev/dataset-import-tasks/" + taskID, "task", "import_task_id", taskID,
			&modeldevv1.GetDatasetImportTaskResponse{Task: &modeldevv1.DatasetImportTaskView{ImportTaskId: taskID}}},
		{"Preview", "/api/v1/modeldev/input-versions/" + versionID + "/preview", "preview", "input_version_id", versionID,
			&modeldevv1.GetInputVersionPreviewResponse{Preview: &modeldevv1.DatasetPreviewView{InputVersionId: versionID}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			srv := khttp.NewServer(khttp.Middleware(func(next middleware.Handler) middleware.Handler {
				return func(ctx context.Context, request interface{}) (interface{}, error) {
					return test.response, nil
				}
			}))
			adminv1.RegisterNetworkServiceHTTPServer(srv, nil)
			registerModelDevHTTP(srv, nil)
			web := httptest.NewServer(srv)
			defer web.Close()

			response, err := web.Client().Get(web.URL + test.path)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, http.StatusOK, response.StatusCode)

			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(response.Body).Decode(&body))
			require.Equal(t, test.value, body[test.key])
			require.NotContains(t, body, test.wrapper)
		})
	}
}
