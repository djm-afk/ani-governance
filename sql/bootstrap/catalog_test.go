package bootstrap

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/stretchr/testify/require"

	"go-wind-admin/pkg/permcode"
)

func TestCatalogUsesChineseDisplayMetadata(t *testing.T) {
	document, err := os.ReadFile("../../app/admin/service/cmd/server/assets/openapi.yaml")
	require.NoError(t, err)
	doc, err := openapi3.NewLoader().LoadFromData(document)
	require.NoError(t, err)
	catalog, err := Catalog(document)
	require.NoError(t, err)
	var descriptions map[string]string
	require.NoError(t, json.Unmarshal(apiDescriptionsJSON, &descriptions))
	require.Len(t, descriptions, len(catalog), "display metadata must cover exactly the released API catalog")
	byEndpoint := make(map[string]API, len(catalog))
	for _, api := range catalog {
		key := api.Method + " " + api.Path
		require.Contains(t, descriptions, key)
		require.Empty(t, api.ModuleDescription, key)
		require.Regexp(t, `[一-龥]`, api.Description, key)
		require.LessOrEqual(t, utf8.RuneCountInString(api.Description), 28, key)
		operation := doc.Paths.Value(api.Path).Operations()[api.Method]
		require.NotNil(t, operation)
		require.Equal(t, operation.OperationID, api.Operation, key)
		require.Equal(t, operation.Tags[0], api.Module, key)
		byEndpoint[key] = api
	}
	require.Len(t, byEndpoint, len(catalog), "catalog endpoints must stay unique")
	require.Equal(t, "查询容器实例列表", byEndpoint["GET /api/v1/instances"].Description)
	require.Equal(t, "创建虚拟机", byEndpoint["POST /api/v1/virtual-machines"].Description)
	require.Equal(t, "按编码查询字典类型", byEndpoint["GET /api/v1/admin/dict/types/code/{code}"].Description)
	require.Equal(t, "查询VPC详情", byEndpoint["GET /api/v1/networks/vpcs/{vpc_id}"].Description)
	require.Contains(t, doc.Paths.Value("/api/v1/networks/vpcs/{vpc_id}").Get.Description, "network:vpc:get",
		"short management labels must not replace the detailed OpenAPI contract")
}

func TestCatalogValidation(t *testing.T) {
	_, err := Catalog([]byte("openapi: 3.0.0\ninfo: {title: test, version: '1'}\n"))
	require.Error(t, err)
	_, err = Catalog([]byte("openapi: 3.0.0\ninfo: {title: test, version: '1'}\npaths:\n  /unknown:\n    get:\n      operationId: Unknown_Get\n      tags: [UnknownService]\n"))
	require.ErrorContains(t, err, "business module")
}

func TestInferenceKeyCatalogIsSeparateFromPlatformKeys(t *testing.T) {
	document, err := os.ReadFile("../../app/admin/service/cmd/server/assets/openapi.yaml")
	require.NoError(t, err)
	catalog, err := Catalog(document)
	require.NoError(t, err)
	expected := map[string]string{
		"POST /api/v1/inference-keys":                      "inference:key:create",
		"GET /api/v1/inference-keys":                       "inference:key:read",
		"GET /api/v1/inference-keys/{key_id}":              "inference:key:read",
		"DELETE /api/v1/inference-keys/{key_id}":           "inference:key:revoke",
		"POST /api/v1/inference-keys/{key_id}:rotate":      "inference:key:rotate",
		"POST /api/v1/inference-keys/{key_id}:bind-policy": "inference:key:bind-policy",
		"POST /api/v1/inference-policies":                  "inference:policy:create",
		"GET /api/v1/inference-policies":                   "inference:policy:read",
		"GET /api/v1/inference-policies/{policy_id}":       "inference:policy:read",
		"PUT /api/v1/inference-policies/{policy_id}":       "inference:policy:update",
		"DELETE /api/v1/inference-policies/{policy_id}":    "inference:policy:delete",
	}
	count, platformCount := 0, 0
	for _, route := range catalog {
		if strings.HasPrefix(route.Path, "/api/v1/auth/api-keys") {
			platformCount++
			require.Equal(t, "AccessKeyService", route.Module)
			require.Equal(t, "SYSTEM", route.BusinessModule)
		}
		if route.Module != "InferenceKeyService" {
			continue
		}
		count++
		require.Equal(t, "INFERENCE", route.BusinessModule)
		grant, err := permcode.Resolve(route.Method, route.Path)
		require.NoError(t, err)
		require.Equal(t, expected[route.Method+" "+route.Path], grant.Code)
	}
	require.Equal(t, len(expected), count)
	require.Equal(t, 6, platformCount)
}

// The deployment catalog must describe the actual Kratos path variables. A
// camelCase OpenAPI alias creates a different DB authorization route even when
// the public URL is the same, preventing the tenant from reading its own VM.
func TestVMCatalogUsesRuntimePathVariables(t *testing.T) {
	document, err := os.ReadFile("../../app/admin/service/cmd/server/assets/openapi.yaml")
	require.NoError(t, err)
	catalog, err := Catalog(document)
	require.NoError(t, err)
	count := 0
	for _, route := range catalog {
		if route.Module != "VMService" {
			continue
		}
		count++
		require.NotContains(t, route.Path, "{vmId}")
		require.NotContains(t, route.Path, "{operationId}")
		require.NotContains(t, route.Path, "{snapshotId}")
		resolved, err := permcode.Resolve(route.Method, route.Path)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(resolved.Code, "vm:"))
	}
	// The retained OpenAPI has 27 register-vm-permissions.sql routes plus
	// SetVMSecurityGroups and two explicit network-interface owner routes.
	require.Equal(t, 30, count)
}
