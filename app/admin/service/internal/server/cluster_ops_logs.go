package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	clusteropsv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/clusterops/v1"
	catalogv1 "go-wind-admin/api/gen/go/catalog/service/v1"
	"go-wind-admin/app/admin/service/internal/service"
	"go-wind-admin/pkg/middleware/auth"
	"go-wind-admin/pkg/middleware/requestid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const clusterOpsLogsPath = "/api/v1/admin/cluster-ops/clusters/{cluster_id}/namespaces/{namespace}/pods/{pod_name}/logs"
const clusterOpsLogsOperation = auth.ClusterOpsOperationPrefix + "StreamPodLogs"

type clusterOpsLogHTTP struct {
	service *service.PlatformClusterOpsService
}

func RegisterClusterOpsLogsHTTP(server *khttp.Server, s *service.PlatformClusterOpsService) {
	if s != nil {
		server.Route("/").GET(clusterOpsLogsPath, (&clusterOpsLogHTTP{service: s}).handle)
	}
}

func (h *clusterOpsLogHTTP) handle(ctx khttp.Context) error {
	khttp.SetOperation(ctx, clusterOpsLogsOperation)
	_, err := ctx.Middleware(func(call context.Context, _ any) (any, error) {
		req, err := decodeClusterOpsLogQuery(ctx.Request(), ctx.Vars())
		if err != nil {
			return nil, err
		}
		return nil, h.stream(call, ctx, req)
	})(ctx, nil)
	// stream returns errors only before committing SSE. Once committed, it owns
	// every termination and never delegates to the ordinary JSON error encoder.
	return err
}

func decodeClusterOpsLogQuery(r *http.Request, vars url.Values) (*catalogv1.ClusterOpsStreamPodLogsRequest, error) {
	if err := validateClusterOpsQuery(r, clusterOpsLogsOperation); err != nil {
		return nil, err
	}
	req := &catalogv1.ClusterOpsStreamPodLogsRequest{
		ClusterId: vars.Get("cluster_id"), Namespace: vars.Get("namespace"), PodName: vars.Get("pod_name"),
	}
	for _, value := range []string{req.ClusterId, req.Namespace, req.PodName} {
		if !clusterOpsValidSegment(value) {
			return nil, invalidClusterOpsRequest()
		}
	}
	values := r.URL.Query()
	req.Container = values.Get("container")
	req.ExpectedPodUid = values.Get("expected_pod_uid")
	req.Follow = values.Get("follow") == "true"
	req.Previous = values.Get("previous") == "true"
	req.Timestamps = values.Get("timestamps") == "true"
	for _, item := range []struct {
		name   string
		target **int64
	}{
		{"tail_lines", &req.TailLines}, {"since_seconds", &req.SinceSeconds}, {"limit_bytes", &req.LimitBytes},
	} {
		if raw, exists := values[item.name]; exists {
			number, err := strconv.ParseInt(raw[0], 10, 64)
			if err != nil {
				return nil, invalidClusterOpsRequest()
			}
			*item.target = &number
		}
	}
	if raw, exists := values["since_time"]; exists {
		value, err := time.Parse(time.RFC3339Nano, raw[0])
		if err != nil {
			return nil, invalidClusterOpsRequest()
		}
		req.SinceTime = timestamppb.New(value)
		if req.SinceTime.CheckValid() != nil {
			return nil, invalidClusterOpsRequest()
		}
	}
	return req, nil
}

type clusterOpsReceived struct {
	frame     *clusteropsv1.PodLogFrame
	err       error
	openError bool
}

// A single receive goroutine hands at most one queued frame to the handler.
// Opening the RPC is supervised by the same READY timer without replacing or
// canceling the context the live stream inherits.
func (h *clusterOpsLogHTTP) receive(ctx context.Context, req *catalogv1.ClusterOpsStreamPodLogsRequest) <-chan clusterOpsReceived {
	received := make(chan clusterOpsReceived, 1)
	go func() {
		defer close(received)
		stream, err := h.service.OpenPodLogs(ctx, req)
		if err != nil {
			select {
			case received <- clusterOpsReceived{err: err, openError: true}:
			case <-ctx.Done():
			}
			return
		}
		for {
			frame, err := stream.Recv()
			select {
			case received <- clusterOpsReceived{frame: frame, err: err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return received
}

func clusterOpsProtocolError() error {
	return kerrors.New(http.StatusBadGateway, "LOG_STREAM_PROTOCOL_ERROR", "invalid upstream log stream")
}
func clusterOpsInterruptedError() error {
	return kerrors.New(http.StatusServiceUnavailable, "LOG_STREAM_INTERRUPTED", "log stream interrupted; reconnect explicitly")
}

func (h *clusterOpsLogHTTP) stream(call context.Context, ctx khttp.Context, req *catalogv1.ClusterOpsStreamPodLogsRequest) error {
	streamCtx, cancel := context.WithTimeout(call, 10*time.Minute+15*time.Second)
	defer cancel()
	frames := h.receive(streamCtx, req)
	readyTimer := time.NewTimer(35 * time.Second)
	defer readyTimer.Stop()
	var first clusterOpsReceived
	select {
	case <-call.Done():
		return service.MapClusterOpsError(call.Err())
	case <-readyTimer.C:
		return service.MapClusterOpsError(context.DeadlineExceeded)
	case first = <-frames:
	}
	if first.err != nil {
		if first.openError {
			return first.err
		}
		if errors.Is(first.err, io.EOF) {
			return clusterOpsInterruptedError()
		}
		return service.MapClusterOpsError(first.err)
	}
	ready := first.frame
	if ready == nil || ready.Type != clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_READY || ready.PodUid == "" || ready.Container == "" {
		return clusterOpsProtocolError()
	}
	readyTimer.Stop()
	response := ctx.Response()
	controller := http.NewResponseController(response)
	if controller.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
		return kerrors.New(http.StatusServiceUnavailable, "CLUSTEROPS_UNAVAILABLE", "stream transport does not support bounded writes")
	}
	defer controller.SetWriteDeadline(time.Time{})
	response.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Accel-Buffering", "no")
	if id, ok := requestid.FromContext(call); ok {
		response.Header().Set(requestid.HeaderRequestID, id)
	}
	response.WriteHeader(http.StatusOK)
	writer := clusterOpsSSEWriter{response, controller}
	if writer.event("ready", map[string]string{"pod_uid": ready.PodUid, "container": ready.Container}) != nil {
		return nil
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	fail := func(err error) { _ = writer.event("error", clusterOpsErrorPayload(call, err)) }
	for {
		select {
		case <-call.Done():
			if errors.Is(call.Err(), context.DeadlineExceeded) {
				fail(service.MapClusterOpsError(call.Err()))
			}
			return nil
		case <-streamCtx.Done():
			if call.Err() == nil {
				fail(service.MapClusterOpsError(streamCtx.Err()))
			}
			return nil
		case <-heartbeat.C:
			if writer.write(": heartbeat\n\n") != nil {
				return nil
			}
		case received, ok := <-frames:
			if call.Err() != nil && !errors.Is(call.Err(), context.DeadlineExceeded) {
				return nil
			}
			if !ok || received.err != nil {
				if received.err != nil && (status.Code(received.err) == codes.DeadlineExceeded || errors.Is(received.err, context.DeadlineExceeded)) {
					fail(service.MapClusterOpsError(received.err))
				} else {
					fail(clusterOpsInterruptedError())
				}
				return nil
			}
			frame := received.frame
			if frame == nil {
				fail(clusterOpsProtocolError())
				return nil
			}
			switch frame.Type {
			case clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_DATA:
				if len(frame.Data) > 16<<10 {
					fail(clusterOpsProtocolError())
					return nil
				}
				if writer.event("data", map[string]string{"data_base64": base64.StdEncoding.EncodeToString(frame.Data)}) != nil {
					return nil
				}
			case clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_END:
				if frame.Reason != "EOF" && frame.Reason != "STREAM_LIMIT_REACHED" {
					fail(clusterOpsProtocolError())
					return nil
				}
				_ = writer.event("end", map[string]string{"reason": frame.Reason})
				return nil
			case clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_ERROR:
				fail(service.ClusterOpsLogFrameError(frame.Reason))
				return nil
			default:
				fail(clusterOpsProtocolError())
				return nil
			}
		}
	}
}

type clusterOpsErrorBody struct {
	Code     int32             `json:"code"`
	Reason   string            `json:"reason"`
	Message  string            `json:"message"`
	Metadata map[string]string `json:"metadata"`
}

func clusterOpsErrorPayload(ctx context.Context, err error) clusterOpsErrorBody {
	known := kerrors.FromError(err)
	metadata := map[string]string{}
	for key, value := range known.Metadata {
		metadata[key] = value
	}
	if id, ok := requestid.FromContext(ctx); ok {
		metadata["request_id"] = id
	}
	return clusterOpsErrorBody{known.Code, known.Reason, known.Message, metadata}
}

type clusterOpsSSEWriter struct {
	response   http.ResponseWriter
	controller *http.ResponseController
}

func (w clusterOpsSSEWriter) event(event string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return w.write(fmt.Sprintf("event: %s\ndata: %s\n\n", event, raw))
}
func (w clusterOpsSSEWriter) write(frame string) error {
	if err := w.controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if _, err := io.WriteString(w.response, frame); err != nil {
		return err
	}
	if err := w.controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	return w.controller.Flush()
}
