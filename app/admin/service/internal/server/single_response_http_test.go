package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-kratos/kratos/v2/middleware"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"github.com/stretchr/testify/require"
	admin "go-wind-admin/api/gen/go/admin/service/v1"
	catalog "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/app/admin/service/cmd/server/assets"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

// These transport contract assertions complement the real authenticated PG
// joint suites. They verify both generated HTTP directions and the OpenAPI.
func TestSingleWriteHTTPResponsesAndGeneratedClients(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name, method, path, wrapper, schema string
		response                            proto.Message
		call                                func(*khttp.Client) (proto.Message, error)
	}{
		{"CreateVNic", "POST", "/api/v1/networks/vnics", "vnic", "VNic", &catalog.VNicResponse{Vnic: &catalog.VNic{Id: "vnic_x", Version: 7}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewNetworkServiceHTTPClient(c).CreateVNic(ctx, &catalog.CreateVNicRequest{})
		}},
		{"DeleteVNic", "DELETE", "/api/v1/networks/vnics/vnic_x", "vnic", "VNic", &catalog.VNicResponse{Vnic: &catalog.VNic{Id: "vnic_x", State: "deleting"}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewNetworkServiceHTTPClient(c).DeleteVNic(ctx, &catalog.DeleteVNicRequest{VnicId: "vnic_x"})
		}},
		{"CreateVPC", "POST", "/api/v1/networks/vpcs", "vpc", "VPC", &catalog.CreateVPCResponse{Vpc: &catalog.VPC{Id: "vpc_x", Version: 7}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewNetworkServiceHTTPClient(c).CreateVPC(ctx, &catalog.CreateVPCRequest{})
		}},
		{"DeleteVPC", "DELETE", "/api/v1/networks/vpcs/vpc_x", "vpc", "VPC", &catalog.DeleteVPCResponse{Vpc: &catalog.VPC{Id: "vpc_x", State: "deleting"}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewNetworkServiceHTTPClient(c).DeleteVPC(ctx, &catalog.DeleteVPCRequest{VpcId: "vpc_x"})
		}},
		{"CreateEIP", "POST", "/api/v1/networks/eips", "eip", "EIP", &catalog.CreateEIPResponse{Eip: &catalog.EIP{Id: "eip_x", Address: "192.0.2.1"}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewNetworkServiceHTTPClient(c).CreateEIP(ctx, &catalog.CreateEIPRequest{})
		}},
		{"DeleteEIP", "DELETE", "/api/v1/networks/eips/eip_x", "eip", "EIP", &catalog.DeleteEIPResponse{Eip: &catalog.EIP{Id: "eip_x", State: "deleting"}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewNetworkServiceHTTPClient(c).DeleteEIP(ctx, &catalog.DeleteEIPRequest{EipId: "eip_x"})
		}},
		{"BindVPCSnat", "POST", "/api/v1/networks/vpcs/vpc_x/snat/bindings", "snat", "VPCSnat", &catalog.BindVPCSnatResponse{Snat: &catalog.VPCSnat{Id: "snat_x", DesiredEnabled: true}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewNetworkServiceHTTPClient(c).BindVPCSnat(ctx, &catalog.BindVPCSnatRequest{VpcId: "vpc_x"})
		}},
		{"EnsureImageSpace", "POST", "/api/v1/images/space:enable", "space", "ImageSpace", &catalog.EnsureImageSpaceResponse{Space: &catalog.ImageSpace{SpaceId: "space_x", Version: 7}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewImageServiceHTTPClient(c).EnsureImageSpace(ctx, &catalog.EnsureImageSpaceRequest{})
		}},
		{"DisablePublisherCredential", "POST", "/api/v1/images/publisher-credential:disable", "credential", "PublisherCredential", &catalog.DisablePublisherCredentialResponse{Credential: &catalog.PublisherCredential{Username: "publisher", State: "disabled", Version: 7}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewImageServiceHTTPClient(c).DisablePublisherCredential(ctx, &catalog.DisablePublisherCredentialRequest{})
		}},
		{"RegisterImage", "POST", "/api/v1/images/registrations", "image", "ImageRegistration", &catalog.RegisterImageResponse{Image: &catalog.ImageRegistration{ImageId: "img_x", Version: 7, Platforms: []*catalog.ImagePlatform{{Os: "linux", Architecture: "amd64"}}}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewImageServiceHTTPClient(c).RegisterImage(ctx, &catalog.RegisterImageRequest{})
		}},
		{"UpdateImage", "PATCH", "/api/v1/images/registrations/img_x", "image", "ImageRegistration", &catalog.UpdateImageResponse{Image: &catalog.ImageRegistration{ImageId: "img_x", DisplayName: "edited", Version: 7}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewImageServiceHTTPClient(c).UpdateImage(ctx, &catalog.UpdateImageRequest{ImageId: "img_x"})
		}},
		{"UnregisterImage", "POST", "/api/v1/images/registrations/img_x:unregister", "image", "ImageRegistration", &catalog.UnregisterImageResponse{Image: &catalog.ImageRegistration{ImageId: "img_x", Version: 7}}, func(c *khttp.Client) (proto.Message, error) {
			return admin.NewImageServiceHTTPClient(c).UnregisterImage(ctx, &catalog.UnregisterImageRequest{ImageId: "img_x"})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := khttp.NewServer(khttp.Middleware(func(next middleware.Handler) middleware.Handler {
				return func(context.Context, interface{}) (interface{}, error) { return tc.response, nil }
			}))
			admin.RegisterNetworkServiceHTTPServer(srv, nil)
			admin.RegisterImageServiceHTTPServer(srv, nil)
			web := httptest.NewServer(srv)
			defer web.Close()
			req, err := http.NewRequest(tc.method, web.URL+tc.path, strings.NewReader("{}"))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			reply, err := web.Client().Do(req)
			require.NoError(t, err)
			defer reply.Body.Close()
			require.Equal(t, 200, reply.StatusCode)
			var body map[string]any
			require.NoError(t, json.NewDecoder(reply.Body).Decode(&body))
			require.NotContains(t, body, tc.wrapper)
			require.NotContains(t, body, "tenant_id")
			require.NotContains(t, body, "code")
			require.NotContains(t, body, "data")
			if tc.name == "CreateVPC" || tc.name == "EnsureImageSpace" {
				require.Equal(t, "7", body["version"])
			}
			if tc.name == "RegisterImage" {
				require.Len(t, body["platforms"], 1, "domain nesting remains")
			}
			client, err := khttp.NewClient(ctx, khttp.WithEndpoint(web.URL))
			require.NoError(t, err)
			defer client.Close()
			decoded, err := tc.call(client)
			require.NoError(t, err)
			require.True(t, proto.Equal(tc.response, decoded), "generated client must reconstruct the internal response from the flat public object")
		})
	}
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(assets.OpenApiData, &doc), "duplicate response mapping keys are invalid")
	paths := doc["paths"].(map[string]any)
	for _, tc := range tests {
		p := tc.path
		p = strings.ReplaceAll(p, "vnic_x", "{vnic_id}")
		p = strings.ReplaceAll(p, "vpc_x", "{vpc_id}")
		p = strings.ReplaceAll(p, "eip_x", "{eip_id}")
		p = strings.ReplaceAll(p, "img_x", "{image_id}")
		operation := paths[p].(map[string]any)[strings.ToLower(tc.method)].(map[string]any)
		responses := operation["responses"].(map[string]any)
		response := responses["200"].(map[string]any)
		schema := response["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		require.Equal(t, "#/components/schemas/"+tc.schema, schema["$ref"], tc.name)
	}
}

func TestBusinessCombinationHTTPResponsesKeepFields(t *testing.T) {
	tests := []struct {
		path     string
		response proto.Message
		fields   []string
	}{
		{"/api/v1/images/publisher-credential:issue", &catalog.IssuePublisherCredentialResponse{Credential: &catalog.PublisherCredential{Username: "publisher"}, Secret: "test-only-secret"}, []string{"credential", "secret", "replayUntil"}},
		{"/api/v1/images/publisher-credential:reset", &catalog.ResetPublisherCredentialResponse{Credential: &catalog.PublisherCredential{Username: "publisher"}, Secret: "test-only-secret"}, []string{"credential", "secret", "replayUntil"}},
		{"/api/v1/networks/load-balancers", &catalog.CreateLoadBalancerResponse{LoadBalancer: &catalog.LoadBalancer{Id: "lb_x"}, Operation: &catalog.Operation{Id: "operation_x"}}, []string{"load_balancer", "operation"}},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			srv := khttp.NewServer(khttp.Middleware(func(next middleware.Handler) middleware.Handler {
				return func(context.Context, interface{}) (interface{}, error) { return tc.response, nil }
			}))
			admin.RegisterNetworkServiceHTTPServer(srv, nil)
			admin.RegisterImageServiceHTTPServer(srv, nil)
			web := httptest.NewServer(srv)
			defer web.Close()
			req, err := http.NewRequest("POST", web.URL+tc.path, strings.NewReader("{}"))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			reply, err := web.Client().Do(req)
			require.NoError(t, err)
			defer reply.Body.Close()
			require.Equal(t, 200, reply.StatusCode)
			var body map[string]any
			require.NoError(t, json.NewDecoder(reply.Body).Decode(&body))
			for _, field := range tc.fields {
				require.Contains(t, body, field)
			}
		})
	}
}
