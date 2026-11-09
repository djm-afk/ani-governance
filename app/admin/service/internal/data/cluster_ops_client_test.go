package data

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	clusteropsv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/clusterops/v1"
	instancev1 "github.com/zhangzhe-ctrl/ani-resource-service/api/instance/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

type clusterOpsRPCBoundary struct {
	clusteropsv1.UnimplementedPlatformClusterOpsServiceServer
	instancev1.UnimplementedInstanceServiceServer
	streamStopped chan struct{}
}

func (*clusterOpsRPCBoundary) GetInstance(context.Context, *instancev1.GetInstanceRequest) (*instancev1.Instance, error) {
	return &instancev1.Instance{}, nil
}
func (*clusterOpsRPCBoundary) ListClusters(context.Context, *emptypb.Empty) (*clusteropsv1.ListClustersResponse, error) {
	return &clusteropsv1.ListClustersResponse{}, nil
}
func (*clusterOpsRPCBoundary) GetClusterOverview(context.Context, *clusteropsv1.GetClusterOverviewRequest) (*clusteropsv1.ClusterOverview, error) {
	return &clusteropsv1.ClusterOverview{}, nil
}
func (*clusterOpsRPCBoundary) ListResourceTypes(context.Context, *clusteropsv1.ListResourceTypesRequest) (*clusteropsv1.ListResourceTypesResponse, error) {
	return &clusteropsv1.ListResourceTypesResponse{}, nil
}
func (*clusterOpsRPCBoundary) ListResources(context.Context, *clusteropsv1.ListResourcesRequest) (*clusteropsv1.ListResourcesResponse, error) {
	return &clusteropsv1.ListResourcesResponse{}, nil
}
func (*clusterOpsRPCBoundary) GetResource(context.Context, *clusteropsv1.GetResourceRequest) (*clusteropsv1.GetResourceResponse, error) {
	return &clusteropsv1.GetResourceResponse{}, nil
}
func (*clusterOpsRPCBoundary) CreateResource(context.Context, *clusteropsv1.CreateResourceRequest) (*clusteropsv1.MutationResponse, error) {
	return &clusteropsv1.MutationResponse{}, nil
}
func (*clusterOpsRPCBoundary) UpdateResource(context.Context, *clusteropsv1.UpdateResourceRequest) (*clusteropsv1.MutationResponse, error) {
	return &clusteropsv1.MutationResponse{}, nil
}
func (*clusterOpsRPCBoundary) DeleteResource(context.Context, *clusteropsv1.DeleteResourceRequest) (*clusteropsv1.DeleteResourceResponse, error) {
	return &clusteropsv1.DeleteResourceResponse{}, nil
}
func (*clusterOpsRPCBoundary) ScaleWorkload(context.Context, *clusteropsv1.ScaleWorkloadRequest) (*clusteropsv1.ScaleWorkloadResponse, error) {
	return &clusteropsv1.ScaleWorkloadResponse{}, nil
}
func (*clusterOpsRPCBoundary) RestartWorkload(context.Context, *clusteropsv1.RestartWorkloadRequest) (*clusteropsv1.MutationResponse, error) {
	return &clusteropsv1.MutationResponse{}, nil
}
func (s *clusterOpsRPCBoundary) StreamPodLogs(_ *clusteropsv1.StreamPodLogsRequest, stream grpc.ServerStreamingServer[clusteropsv1.PodLogFrame]) error {
	defer close(s.streamStopped)
	if err := stream.Send(&clusteropsv1.PodLogFrame{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_READY, PodUid: "pod-uid", Container: "main"}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return status.FromContextError(stream.Context().Err()).Err()
}

type clusterOpsRPCObservation struct {
	method, connection string
	metadata           metadata.MD
	remaining          time.Duration
}

func clusterOpsObservation(ctx context.Context, method string) clusterOpsRPCObservation {
	md, _ := metadata.FromIncomingContext(ctx)
	remote, _ := peer.FromContext(ctx)
	deadline, _ := ctx.Deadline()
	return clusterOpsRPCObservation{method: method, connection: remote.Addr.String(), metadata: md, remaining: time.Until(deadline)}
}

// The boundary substitutes only Res RPC responses. The production shared
// factory, generated clients, mTLS, metadata, deadline and cancellation are real.
func TestClusterOpsClientSharedConnectionAndBudgets(t *testing.T) {
	certs := resourceClientTestCertificates(t, nil, []string{"ani-network-service"})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	seen := make(chan clusterOpsRPCObservation, 16)
	server := grpc.NewServer(
		grpc.Creds(credentials.NewTLS(certs.server)),
		grpc.UnaryInterceptor(func(ctx context.Context, request any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
			seen <- clusterOpsObservation(ctx, info.FullMethod)
			return next(ctx, request)
		}),
		grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
			seen <- clusterOpsObservation(stream.Context(), info.FullMethod)
			return next(srv, stream)
		}),
	)
	boundary := &clusterOpsRPCBoundary{streamStopped: make(chan struct{})}
	clusteropsv1.RegisterPlatformClusterOpsServiceServer(server, boundary)
	instancev1.RegisterInstanceServiceServer(server, boundary)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-done })
	for key, value := range map[string]string{
		"ANI_INSTANCE_ADDR": listener.Addr().String(), "ANI_INSTANCE_CA": certs.caFile,
		"ANI_INSTANCE_CERT": certs.certFile, "ANI_INSTANCE_KEY": certs.keyFile,
	} {
		t.Setenv(key, value)
	}
	owner, _, client, cleanup, err := NewResourceClientsFromEnv()
	require.NoError(t, err)
	t.Cleanup(cleanup)
	require.NotNil(t, client)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"authorization", "private-sentinel", "x-ani-tenant-id", "forged",
		"x-ani-scope", "tenant", "x-ani-actor", "forged", "x-ani-request-id", "forged",
	))
	const actor = "governance:user:9"
	_, err = owner.Get(ctx, "20000000-0000-4000-8000-000000000002", actor, "instance")
	require.NoError(t, err)
	connection := (<-seen).connection
	requestIDs := make(map[string]bool)
	check := func(observation clusterOpsRPCObservation, method string, maximum time.Duration) {
		t.Helper()
		require.Equal(t, "/clusterops.v1.PlatformClusterOpsService/"+method, observation.method)
		require.Equal(t, connection, observation.connection, "all Resource domains share one mTLS connection")
		require.Equal(t, []string{"platform"}, observation.metadata.Get("x-ani-scope"))
		require.Equal(t, []string{actor}, observation.metadata.Get("x-ani-actor"))
		require.Empty(t, observation.metadata.Get("x-ani-tenant-id"))
		require.Empty(t, observation.metadata.Get("authorization"))
		id := observation.metadata.Get("x-ani-request-id")
		require.Len(t, id, 1)
		parsed, err := uuid.Parse(id[0])
		require.NoError(t, err)
		require.NotEqual(t, uuid.Nil, parsed)
		require.Equal(t, parsed.String(), id[0])
		require.False(t, requestIDs[id[0]], "each downstream call gets a fresh trace ID")
		requestIDs[id[0]] = true
		require.Greater(t, observation.remaining, time.Duration(0))
		require.LessOrEqual(t, observation.remaining, maximum)
	}
	for _, call := range []struct {
		name string
		run  func() error
	}{
		{"ListClusters", func() error { _, e := client.ListClusters(ctx, actor, &emptypb.Empty{}); return e }},
		{"GetClusterOverview", func() error {
			_, e := client.GetClusterOverview(ctx, actor, &clusteropsv1.GetClusterOverviewRequest{})
			return e
		}},
		{"ListResourceTypes", func() error {
			_, e := client.ListResourceTypes(ctx, actor, &clusteropsv1.ListResourceTypesRequest{})
			return e
		}},
		{"ListResources", func() error { _, e := client.ListResources(ctx, actor, &clusteropsv1.ListResourcesRequest{}); return e }},
		{"GetResource", func() error { _, e := client.GetResource(ctx, actor, &clusteropsv1.GetResourceRequest{}); return e }},
		{"CreateResource", func() error {
			_, e := client.CreateResource(ctx, actor, &clusteropsv1.CreateResourceRequest{})
			return e
		}},
		{"UpdateResource", func() error {
			_, e := client.UpdateResource(ctx, actor, &clusteropsv1.UpdateResourceRequest{})
			return e
		}},
		{"DeleteResource", func() error {
			_, e := client.DeleteResource(ctx, actor, &clusteropsv1.DeleteResourceRequest{})
			return e
		}},
		{"ScaleWorkload", func() error { _, e := client.ScaleWorkload(ctx, actor, &clusteropsv1.ScaleWorkloadRequest{}); return e }},
		{"RestartWorkload", func() error {
			_, e := client.RestartWorkload(ctx, actor, &clusteropsv1.RestartWorkloadRequest{})
			return e
		}},
	} {
		require.NoError(t, call.run(), call.name)
		observation := <-seen
		check(observation, call.name, 30*time.Second)
		require.Greater(t, observation.remaining, 25*time.Second)
	}
	short, cancelShort := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancelShort()
	_, err = client.ListClusters(short, actor, &emptypb.Empty{})
	require.NoError(t, err)
	check(<-seen, "ListClusters", 500*time.Millisecond)

	streamCtx, cancelStream := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelStream()
	stream, err := client.StreamPodLogs(streamCtx, actor, &clusteropsv1.StreamPodLogsRequest{})
	require.NoError(t, err)
	frame, err := stream.Recv()
	require.NoError(t, err, "returning the stream must not cancel it")
	require.Equal(t, clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_READY, frame.Type)
	observation := <-seen
	check(observation, "StreamPodLogs", 2*time.Minute)
	require.Greater(t, observation.remaining, time.Minute, "stream must not inherit the 30s unary budget")
	cancelStream()
	select {
	case <-boundary.streamStopped:
	case <-time.After(2 * time.Second):
		t.Fatal("canceling the browser-owned context did not release the downstream stream")
	}
	cleanup()
	_, err = client.ListClusters(ctx, actor, &emptypb.Empty{})
	require.Error(t, err, "the unique cleanup closes every domain client")
	_, err = owner.Get(ctx, "20000000-0000-4000-8000-000000000002", actor, "instance")
	require.Error(t, err)
}

func TestClusterOpsClientRejectsUntrustedActorAndMissingDependency(t *testing.T) {
	for _, actor := range []string{"", "governance:access-key:9", "governance:user:0", "governance:user:09", "governance:user:+9", "governance:user:4294967296", "user:9"} {
		_, err := clusterOpsOutgoingContext(context.Background(), actor)
		require.Equal(t, codes.Unauthenticated, status.Code(err), actor)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := clusterOpsOutgoingContext(ctx, "governance:user:9")
	require.Equal(t, codes.Canceled, status.Code(err))
	t.Setenv("ANI_INSTANCE_ADDR", "")
	owner, compute, clusterOps, cleanup, err := NewResourceClientsFromEnv()
	require.NoError(t, err)
	require.Nil(t, owner)
	require.Nil(t, compute)
	require.Nil(t, clusterOps)
	require.NotNil(t, cleanup)
	cleanup()
	var absent *ClusterOpsClient
	_, err = absent.ListClusters(context.Background(), "governance:user:9", &emptypb.Empty{})
	require.Equal(t, codes.Unimplemented, status.Code(err))
}

// Only the external Res server response is substituted. The shared factory,
// production ListResources method, mTLS and protobuf framing all remain real.
type clusterOpsLargePageBoundary struct {
	clusteropsv1.UnimplementedPlatformClusterOpsServiceServer
	response *clusteropsv1.ListResourcesResponse
	requests chan *clusteropsv1.ListResourcesRequest
}

func (s *clusterOpsLargePageBoundary) ListResources(_ context.Context, req *clusteropsv1.ListResourcesRequest) (*clusteropsv1.ListResourcesResponse, error) {
	s.requests <- req
	return s.response, nil
}

func TestClusterOpsClientListResourcesLargePage(t *testing.T) {
	const namespace = "large-page-fixture"
	payload := strings.Repeat("x", 900*1024)
	response := &clusteropsv1.ListResourcesResponse{
		NextContinueToken:   "native-cursor+/=",
		ListResourceVersion: "9007199254740993",
	}
	for i := 1; i <= 5; i++ {
		name := fmt.Sprintf("large-cm-%d", i)
		uid := fmt.Sprintf("10000000-0000-4000-8000-%012d", i)
		rv := fmt.Sprintf("90071992547410%02d", i)
		raw, err := json.Marshal(map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]string{"name": name, "namespace": namespace, "uid": uid, "resourceVersion": rv},
			"data":     map[string]string{"payload": payload},
		})
		require.NoError(t, err)
		response.Items = append(response.Items, &clusteropsv1.ResourceDocument{
			Ref: &clusteropsv1.ResourceRef{ClusterId: "cluster", Type: &clusteropsv1.ResourceTypeRef{Version: "v1", Resource: "configmaps"}, Namespace: namespace, Name: name},
			Uid: uid, ResourceVersion: rv, ObjectJson: string(raw),
			ReadOnly: true, ReadOnlyReason: "TENANT_NAMESPACE_READ_ONLY",
		})
	}
	require.Greater(t, proto.Size(response), 4*1024*1024, "the protobuf message must cross gRPC's default receive boundary")
	certs := resourceClientTestCertificates(t, nil, []string{"ani-network-service"})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(certs.server)))
	boundary := &clusterOpsLargePageBoundary{response: response, requests: make(chan *clusteropsv1.ListResourcesRequest, 2)}
	clusteropsv1.RegisterPlatformClusterOpsServiceServer(server, boundary)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); <-done })
	for key, value := range map[string]string{
		"ANI_INSTANCE_ADDR": listener.Addr().String(), "ANI_INSTANCE_CA": certs.caFile,
		"ANI_INSTANCE_CERT": certs.certFile, "ANI_INSTANCE_KEY": certs.keyFile,
	} {
		t.Setenv(key, value)
	}
	_, _, client, cleanup, err := NewResourceClientsFromEnv()
	require.NoError(t, err)
	t.Cleanup(cleanup)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := &clusteropsv1.ListResourcesRequest{
		ClusterId: "cluster", Type: &clusteropsv1.ResourceTypeRef{Version: "v1", Resource: "configmaps"},
		Namespace: namespace, PageSize: 50, ContinueToken: "incoming-native-cursor",
		LabelSelector: "fixture=large", FieldSelector: "metadata.namespace=" + namespace,
	}
	actual, err := client.ListResources(ctx, "governance:user:9", req)
	require.NoError(t, err)
	require.Len(t, boundary.requests, 1, "one list call must not scan or retry pages")
	require.True(t, proto.Equal(req, <-boundary.requests), "selectors and native cursor must pass through unchanged")
	require.Len(t, actual.Items, 5)
	require.True(t, proto.Equal(response, actual), "complete documents, UIDs/RVs, read-only fields and page metadata must survive the wire")
	for _, item := range actual.Items {
		var object struct{ Data map[string]string }
		require.NoError(t, json.Unmarshal([]byte(item.ObjectJson), &object))
		require.Len(t, object.Data["payload"], len(payload))
		require.Equal(t, sha256.Sum256([]byte(payload)), sha256.Sum256([]byte(object.Data["payload"])), item.Ref.Name)
	}
	// A bare generated call on the same managed connection must still have the
	// default limit. Only the production ClusterOps list call raises its budget.
	_, err = client.client.ListResources(ctx, req)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	require.Empty(t, status.Convert(err).Details())
	require.Len(t, boundary.requests, 1)
}
