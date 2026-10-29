package data

import (
	"context"
	"net"
	"testing"
	"time"

	usage "github.com/zhangzhe-ctrl/ani-observability-service/api/observability/usage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type resourceUsageTestEndpoint struct {
	usage.UnimplementedResourceUsageServiceServer
	seen chan metadata.MD
}

func (s *resourceUsageTestEndpoint) QueryUsage(ctx context.Context, _ *usage.QueryUsageRequest) (*usage.QueryUsageResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.seen <- md
	return &usage.QueryUsageResponse{}, nil
}
func TestResourceUsageClientPlatformMetadata(t *testing.T) {
	f := resourceClientTestCertificates(t, nil, []string{"ani-observability-service"})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(f.server)))
	endpoint := &resourceUsageTestEndpoint{seen: make(chan metadata.MD, 1)}
	usage.RegisterResourceUsageServiceServer(server, endpoint)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("server did not stop")
		}
	})
	for k, v := range map[string]string{"ANI_OBSERVABILITY_GRPC_ADDR": listener.Addr().String(), "ANI_OBSERVABILITY_CA_FILE": f.caFile, "ANI_OBSERVABILITY_CLIENT_CERT_FILE": f.certFile, "ANI_OBSERVABILITY_CLIENT_KEY_FILE": f.keyFile} {
		t.Setenv(k, v)
	}
	client, close, err := NewObservabilityClientFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(close)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "secret", "x-ani-tenant-id", "forged", "x-ani-actor", "forged", "x-ani-context", "tenant"))
	if _, err = client.QueryUsage(ctx, "governance:user:9", &usage.QueryUsageRequest{}); err != nil {
		t.Fatal(err)
	}
	md := <-endpoint.seen
	if len(md.Get("x-ani-tenant-id")) != 0 || len(md.Get("authorization")) != 0 || len(md.Get("x-ani-context")) != 1 || md.Get("x-ani-context")[0] != "platform" || len(md.Get("x-ani-actor")) != 1 || md.Get("x-ani-actor")[0] != "governance:user:9" {
		t.Fatal(md)
	}
	for _, actor := range []string{"governance:user:0", "governance:user:09", "governance:access-key:9", "governance:user:4294967296"} {
		if _, err = client.QueryUsage(ctx, actor, &usage.QueryUsageRequest{}); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("actor=%s error=%v", actor, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = client.QueryUsage(canceled, "governance:user:9", &usage.QueryUsageRequest{}); status.Code(err) != codes.Canceled {
		t.Fatal(err)
	}
}
