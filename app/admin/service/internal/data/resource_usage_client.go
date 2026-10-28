package data

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	usage "github.com/zhangzhe-ctrl/ani-observability-service/api/observability/usage/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// QueryUsage is platform-only. Metadata is reconstructed, never forwarded from
// an HTTP client, and no tenant header impersonates one of the queried tenants.
func (c *ObservabilityClient) QueryUsage(ctx context.Context, actor string, req *usage.QueryUsageRequest) (*usage.QueryUsageResponse, error) {
	if c == nil || c.usage == nil {
		return nil, status.Error(codes.Unavailable, "resource usage unavailable")
	}
	id := strings.TrimPrefix(actor, "governance:user:")
	n, err := strconv.ParseUint(id, 10, 32)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != id || actor != "governance:user:"+id {
		return nil, status.Error(codes.Unauthenticated, "invalid resource usage caller")
	}
	call, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	call = metadata.NewOutgoingContext(call, metadata.Pairs("x-ani-context", "platform", "x-ani-actor", actor, "x-ani-request-id", uuid.NewString()))
	return c.usage.QueryUsage(call, req, grpc.MaxCallRecvMsgSize(8<<20))
}
