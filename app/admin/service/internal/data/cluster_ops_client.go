package data

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	clusteropsv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/clusterops/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// A default 50-document page can contain about 50 MiB of ConfigMaps. Keep
// response growth bounded without changing the shared connection or treating
// the maximum 200 items as a guarantee that every page fits this byte budget.
const clusterOpsListMaxRecvBytes = 64 * 1024 * 1024

// ClusterOpsClient uses the shared, managed Resource connection. Its caller
// has already authorized the platform user and the exact operation.
type ClusterOpsClient struct {
	client clusteropsv1.PlatformClusterOpsServiceClient
}

func clusterOpsOutgoingContext(ctx context.Context, actor string) (context.Context, error) {
	id, err := strconv.ParseUint(strings.TrimPrefix(actor, "governance:user:"), 10, 32)
	if err != nil || id == 0 || actor != "governance:user:"+strconv.FormatUint(id, 10) {
		return nil, status.Error(codes.Unauthenticated, "invalid trusted platform identity")
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	// Replace all preexisting outgoing identity metadata. Even an empty tenant
	// header would incorrectly turn this platform delegation into a tenant call.
	return metadata.NewOutgoingContext(ctx, metadata.Pairs(
		"x-ani-scope", "platform",
		"x-ani-actor", actor,
		"x-ani-request-id", uuid.NewString(),
	)), nil
}

func clusterOpsUnary[T any](c *ClusterOpsClient, ctx context.Context, actor string, call func(context.Context) (T, error)) (T, error) {
	var zero T
	if c == nil || c.client == nil {
		return zero, status.Error(codes.Unimplemented, "cluster operations unavailable")
	}
	trusted, err := clusterOpsOutgoingContext(ctx, actor)
	if err != nil {
		return zero, err
	}
	trusted, cancel := context.WithTimeout(trusted, 30*time.Second)
	defer cancel()
	return call(trusted)
}

func (c *ClusterOpsClient) ListClusters(ctx context.Context, actor string, req *emptypb.Empty) (*clusteropsv1.ListClustersResponse, error) {
	return clusterOpsUnary(c, ctx, actor, func(ctx context.Context) (*clusteropsv1.ListClustersResponse, error) {
		return c.client.ListClusters(ctx, req)
	})
}

func (c *ClusterOpsClient) GetClusterOverview(ctx context.Context, actor string, req *clusteropsv1.GetClusterOverviewRequest) (*clusteropsv1.ClusterOverview, error) {
	return clusterOpsUnary(c, ctx, actor, func(ctx context.Context) (*clusteropsv1.ClusterOverview, error) {
		return c.client.GetClusterOverview(ctx, req)
	})
}

func (c *ClusterOpsClient) ListResourceTypes(ctx context.Context, actor string, req *clusteropsv1.ListResourceTypesRequest) (*clusteropsv1.ListResourceTypesResponse, error) {
	return clusterOpsUnary(c, ctx, actor, func(ctx context.Context) (*clusteropsv1.ListResourceTypesResponse, error) {
		return c.client.ListResourceTypes(ctx, req)
	})
}

func (c *ClusterOpsClient) ListResources(ctx context.Context, actor string, req *clusteropsv1.ListResourcesRequest) (*clusteropsv1.ListResourcesResponse, error) {
	return clusterOpsUnary(c, ctx, actor, func(ctx context.Context) (*clusteropsv1.ListResourcesResponse, error) {
		return c.client.ListResources(ctx, req, grpc.MaxCallRecvMsgSize(clusterOpsListMaxRecvBytes))
	})
}

func (c *ClusterOpsClient) GetResource(ctx context.Context, actor string, req *clusteropsv1.GetResourceRequest) (*clusteropsv1.GetResourceResponse, error) {
	return clusterOpsUnary(c, ctx, actor, func(ctx context.Context) (*clusteropsv1.GetResourceResponse, error) {
		return c.client.GetResource(ctx, req)
	})
}

func (c *ClusterOpsClient) CreateResource(ctx context.Context, actor string, req *clusteropsv1.CreateResourceRequest) (*clusteropsv1.MutationResponse, error) {
	return clusterOpsUnary(c, ctx, actor, func(ctx context.Context) (*clusteropsv1.MutationResponse, error) {
		return c.client.CreateResource(ctx, req)
	})
}

func (c *ClusterOpsClient) UpdateResource(ctx context.Context, actor string, req *clusteropsv1.UpdateResourceRequest) (*clusteropsv1.MutationResponse, error) {
	return clusterOpsUnary(c, ctx, actor, func(ctx context.Context) (*clusteropsv1.MutationResponse, error) {
		return c.client.UpdateResource(ctx, req)
	})
}

func (c *ClusterOpsClient) DeleteResource(ctx context.Context, actor string, req *clusteropsv1.DeleteResourceRequest) (*clusteropsv1.DeleteResourceResponse, error) {
	return clusterOpsUnary(c, ctx, actor, func(ctx context.Context) (*clusteropsv1.DeleteResourceResponse, error) {
		return c.client.DeleteResource(ctx, req)
	})
}

func (c *ClusterOpsClient) ScaleWorkload(ctx context.Context, actor string, req *clusteropsv1.ScaleWorkloadRequest) (*clusteropsv1.ScaleWorkloadResponse, error) {
	return clusterOpsUnary(c, ctx, actor, func(ctx context.Context) (*clusteropsv1.ScaleWorkloadResponse, error) {
		return c.client.ScaleWorkload(ctx, req)
	})
}

func (c *ClusterOpsClient) RestartWorkload(ctx context.Context, actor string, req *clusteropsv1.RestartWorkloadRequest) (*clusteropsv1.MutationResponse, error) {
	return clusterOpsUnary(c, ctx, actor, func(ctx context.Context) (*clusteropsv1.MutationResponse, error) {
		return c.client.RestartWorkload(ctx, req)
	})
}

func (c *ClusterOpsClient) StreamPodLogs(ctx context.Context, actor string, req *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
	if c == nil || c.client == nil {
		return nil, status.Error(codes.Unimplemented, "cluster operations unavailable")
	}
	trusted, err := clusterOpsOutgoingContext(ctx, actor)
	if err != nil {
		return nil, err
	}
	// The HTTP handler owns the stream deadline and cancellation. In particular,
	// a cancel deferred in this method would kill a successfully opened stream.
	return c.client.StreamPodLogs(trusted, req)
}
