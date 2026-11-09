package server

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/transport"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	clusteropsv1 "github.com/zhangzhe-ctrl/ani-resource-service/api/clusterops/v1"
	authv1 "go-wind-admin/api/gen/go/authentication/service/v1"
	"go-wind-admin/app/admin/service/internal/service"
	"go-wind-admin/pkg/middleware/auth"
	"go-wind-admin/pkg/middleware/requestid"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const clusterOpsTestLogs = "/api/v1/admin/cluster-ops/clusters/configured/namespaces/test-space/pods/test/logs"

type clusterOpsLogClientStub struct {
	service.ClusterOpsPlatformClient
	open func(context.Context, string, *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error)
}

func (c *clusterOpsLogClientStub) StreamPodLogs(ctx context.Context, actor string, req *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
	return c.open(ctx, actor, req)
}

// Only the published downstream stream is replaced. The real service actor,
// query decoder, middleware, HTTP route and SSE writer run in every fixture.
type clusterOpsScriptedStream struct {
	grpc.ClientStream
	ctx      context.Context
	frames   []*clusteropsv1.PodLogFrame
	index    int
	terminal error
	stopped  chan struct{}
	once     sync.Once
}

func (s *clusterOpsScriptedStream) Context() context.Context { return s.ctx }
func (s *clusterOpsScriptedStream) Recv() (*clusteropsv1.PodLogFrame, error) {
	if s.index < len(s.frames) {
		frame := s.frames[s.index]
		s.index++
		return frame, nil
	}
	if s.terminal != nil {
		return nil, s.terminal
	}
	<-s.ctx.Done()
	if s.stopped != nil {
		s.once.Do(func() { close(s.stopped) })
	}
	return nil, s.ctx.Err()
}

func clusterOpsLogPrincipal() *auth.Principal {
	claims := &authv1.UserTokenPayload{UserId: 9, Roles: []string{"platform:admin"}, Permissions: []string{"cluster:ops:logs"}}
	return &auth.Principal{Type: auth.SubjectUser, ID: 9, Claims: claims, Roles: claims.Roles}
}

func clusterOpsLogServer(principal *auth.Principal, client service.ClusterOpsPlatformClient, extra ...middleware.Middleware) *khttp.Server {
	middlewares := []middleware.Middleware{
		requestid.Server(),
		func(next middleware.Handler) middleware.Handler {
			return func(ctx context.Context, req any) (any, error) {
				if principal != nil {
					ctx = auth.NewPrincipalContext(ctx, principal)
				}
				return next(ctx, req)
			}
		},
	}
	middlewares = append(middlewares, extra...)
	server := khttp.NewServer(khttp.Timeout(0),
		khttp.Filter(clusterOpsHTTPFilter, instanceHTTPTimeoutFilter(time.Second)),
		khttp.ErrorEncoder(writeClusterOpsHTTPError), khttp.Middleware(middlewares...))
	RegisterClusterOpsLogsHTTP(server, service.NewPlatformClusterOpsService(client))
	return server
}

func clusterOpsReadyFrame() *clusteropsv1.PodLogFrame {
	return &clusteropsv1.PodLogFrame{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_READY, PodUid: "pod-uid", Container: "main"}
}

func clusterOpsWaitValue[T any](t *testing.T, values <-chan T) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("expected downstream boundary was not reached")
		var zero T
		return zero
	}
}

func clusterOpsWaitCanceled(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("downstream stream context was not canceled")
	}
}

type clusterOpsTestEvent struct {
	name string
	data json.RawMessage
}

func clusterOpsReadEvents(t *testing.T, response *http.Response) ([]clusterOpsTestEvent, string) {
	t.Helper()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var events []clusterOpsTestEvent
	for _, block := range strings.Split(strings.TrimSuffix(string(raw), "\n\n"), "\n\n") {
		if block == "" || strings.HasPrefix(block, ":") {
			continue
		}
		lines := strings.Split(block, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") || !json.Valid([]byte(strings.TrimPrefix(lines[1], "data: "))) {
			t.Fatalf("invalid SSE block or appended ordinary JSON: %q", block)
		}
		events = append(events, clusterOpsTestEvent{strings.TrimPrefix(lines[0], "event: "), json.RawMessage(strings.TrimPrefix(lines[1], "data: "))})
	}
	return events, string(raw)
}

func TestClusterOpsLogQueryPreservesOptionalZeroAndTarget(t *testing.T) {
	query := url.Values{"tail_lines": {"0"}, "follow": {"true"}, "previous": {"true"}, "timestamps": {"false"}, "expected_pod_uid": {"pod-uid"}}
	req, err := decodeClusterOpsLogQuery(httptest.NewRequest("GET", clusterOpsTestLogs+"?"+query.Encode(), nil), url.Values{"cluster_id": {"configured"}, "namespace": {"test-space"}, "pod_name": {"test"}})
	if err != nil || req.TailLines == nil || *req.TailLines != 0 || !req.Follow || !req.Previous || req.Timestamps || req.ExpectedPodUid != "pod-uid" || req.SinceSeconds != nil || req.LimitBytes != nil {
		t.Fatalf("optional log query was changed: req=%+v err=%v", req, err)
	}
	if req.ClusterId != "configured" || req.Namespace != "test-space" || req.PodName != "test" {
		t.Fatalf("target fields were changed: %+v", req)
	}
}

func TestClusterOpsLogsPreserveBytesAndOneTerminalFrame(t *testing.T) {
	original := []byte("中文\n\n重复\n重复\n")
	chunks := [][]byte{original[:1], original[1:5], original[5:]}
	opened := make(chan context.Context, 1)
	var middlewareCalls atomic.Int32
	client := &clusterOpsLogClientStub{open: func(ctx context.Context, actor string, req *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
		if actor != "governance:user:9" || req.ExpectedPodUid != "pod-uid" || req.TailLines == nil || *req.TailLines != 0 {
			return nil, errors.New("trusted target/actor or optional zero was lost")
		}
		opened <- ctx
		frames := []*clusteropsv1.PodLogFrame{clusterOpsReadyFrame()}
		for _, chunk := range chunks {
			frames = append(frames, &clusteropsv1.PodLogFrame{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_DATA, Data: chunk})
		}
		frames = append(frames, &clusteropsv1.PodLogFrame{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_END, Reason: "EOF"}, &clusteropsv1.PodLogFrame{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_ERROR, Reason: "LOG_READ_FAILED"})
		return &clusterOpsScriptedStream{ctx: ctx, frames: frames}, nil
	}}
	server := httptest.NewServer(clusterOpsLogServer(clusterOpsLogPrincipal(), client, func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			middlewareCalls.Add(1)
			tr, ok := transport.FromServerContext(ctx)
			if !ok || tr.Operation() != clusterOpsLogsOperation {
				return nil, errors.New("stream operation did not pass through middleware")
			}
			return next(ctx, req)
		}
	}))
	defer server.Close()
	server.Client().Timeout = 2 * time.Second
	response, err := server.Client().Get(server.URL + clusterOpsTestLogs + "?expected_pod_uid=pod-uid&tail_lines=0&follow=true")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Accel-Buffering") != "no" || response.Header.Get(requestid.HeaderRequestID) == "" {
		t.Fatalf("wrong stream handshake: status=%d headers=%v", response.StatusCode, response.Header)
	}
	events, raw := clusterOpsReadEvents(t, response)
	if len(events) != 5 || events[0].name != "ready" || events[4].name != "end" {
		t.Fatalf("wrong event sequence: %s", raw)
	}
	var combined []byte
	for _, event := range events[1:4] {
		var data struct {
			Base64 string `json:"data_base64"`
		}
		if event.name != "data" || json.Unmarshal(event.data, &data) != nil {
			t.Fatalf("invalid byte frame: %+v", event)
		}
		chunk, err := base64.StdEncoding.DecodeString(data.Base64)
		if err != nil {
			t.Fatal(err)
		}
		combined = append(combined, chunk...)
	}
	if string(combined) != string(original) || strings.Contains(raw, "中文") || middlewareCalls.Load() != 1 {
		t.Fatalf("bytes, repeated/blank lines or middleware changed: bytes=%q calls=%d", combined, middlewareCalls.Load())
	}
	ctx := clusterOpsWaitValue(t, opened)
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) < 10*time.Minute || time.Until(deadline) > 10*time.Minute+15*time.Second {
		t.Fatalf("stream did not receive an independent finite lifetime: %v", time.Until(deadline))
	}
	clusterOpsWaitCanceled(t, ctx)
}

func TestClusterOpsLogsFailBeforeReadyWithoutCommittingSSE(t *testing.T) {
	upstream, err := status.New(codes.Unauthenticated, "private credential sentinel").WithDetails(&errdetails.ErrorInfo{Domain: "clusterops.ani.io", Reason: "KUBERNETES_UNAUTHORIZED"})
	if err != nil {
		t.Fatal(err)
	}
	tenant := clusterOpsLogPrincipal()
	tenantID := uint32(99)
	tenant.TenantID, tenant.Claims.TenantId = tenantID, &tenantID
	apiKey := clusterOpsLogPrincipal()
	apiKey.Type = auth.SubjectAPIKey
	for _, tc := range []struct {
		name      string
		principal *auth.Principal
		client    service.ClusterOpsPlatformClient
		status    int
		reason    string
	}{
		{"not logged in", nil, &clusterOpsLogClientStub{}, 401, "INVALID_LOGIN"},
		{"tenant", tenant, &clusterOpsLogClientStub{}, 403, "PLATFORM_CLUSTEROPS_DENIED"},
		{"API Key", apiKey, &clusterOpsLogClientStub{}, 403, "PLATFORM_CLUSTEROPS_DENIED"},
		{"not configured", clusterOpsLogPrincipal(), nil, 503, "CLUSTEROPS_UNAVAILABLE"},
		{"downstream credentials", clusterOpsLogPrincipal(), &clusterOpsLogClientStub{open: func(context.Context, string, *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
			return nil, upstream.Err()
		}}, 502, "KUBERNETES_UNAUTHORIZED"},
		{"first DATA", clusterOpsLogPrincipal(), &clusterOpsLogClientStub{open: func(ctx context.Context, _ string, _ *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
			return &clusterOpsScriptedStream{ctx: ctx, frames: []*clusteropsv1.PodLogFrame{{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_DATA, Data: []byte("private log sentinel")}}}, nil
		}}, 502, "LOG_STREAM_PROTOCOL_ERROR"},
		{"initial EOF", clusterOpsLogPrincipal(), &clusterOpsLogClientStub{open: func(ctx context.Context, _ string, _ *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
			return &clusterOpsScriptedStream{ctx: ctx, terminal: io.EOF}, nil
		}}, 503, "LOG_STREAM_INTERRUPTED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(clusterOpsLogServer(tc.principal, tc.client))
			defer server.Close()
			server.Client().Timeout = 2 * time.Second
			response, err := server.Client().Get(server.URL + clusterOpsTestLogs)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, _ := io.ReadAll(response.Body)
			var body clusterOpsErrorBody
			if err := json.Unmarshal(raw, &body); err != nil || response.StatusCode != tc.status || body.Reason != tc.reason || strings.Contains(response.Header.Get("Content-Type"), "event-stream") || strings.Contains(string(raw), "sentinel") {
				t.Fatalf("status=%d error=%v body=%s", response.StatusCode, err, raw)
			}
		})
	}
}

func TestClusterOpsLogsTerminateOnceAndNeverAppendOrdinaryJSON(t *testing.T) {
	for _, tc := range []struct {
		name, event, reason string
		frames              []*clusteropsv1.PodLogFrame
		terminal            error
	}{
		{"normal limit", "end", "STREAM_LIMIT_REACHED", []*clusteropsv1.PodLogFrame{{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_END, Reason: "STREAM_LIMIT_REACHED"}}, nil},
		{"domain ERROR then EOF", "error", "LOG_READ_FAILED", []*clusteropsv1.PodLogFrame{{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_ERROR, Reason: "LOG_READ_FAILED", Message: "private secret sentinel"}}, io.EOF},
		{"missing terminal EOF", "error", "LOG_STREAM_INTERRUPTED", nil, io.EOF},
		{"duplicate READY", "error", "LOG_STREAM_PROTOCOL_ERROR", []*clusteropsv1.PodLogFrame{clusterOpsReadyFrame()}, nil},
		{"oversized DATA", "error", "LOG_STREAM_PROTOCOL_ERROR", []*clusteropsv1.PodLogFrame{{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_DATA, Data: make([]byte, (16<<10)+1)}}, nil},
		{"unknown frame", "error", "LOG_STREAM_PROTOCOL_ERROR", []*clusteropsv1.PodLogFrame{{Type: clusteropsv1.PodLogFrameType(99)}}, nil},
		{"invalid end reason", "error", "LOG_STREAM_PROTOCOL_ERROR", []*clusteropsv1.PodLogFrame{{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_END, Reason: "UNKNOWN"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := make(chan context.Context, 1)
			client := &clusterOpsLogClientStub{open: func(ctx context.Context, _ string, _ *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
				opened <- ctx
				return &clusterOpsScriptedStream{ctx: ctx, frames: append([]*clusteropsv1.PodLogFrame{clusterOpsReadyFrame()}, tc.frames...), terminal: tc.terminal}, nil
			}}
			server := httptest.NewServer(clusterOpsLogServer(clusterOpsLogPrincipal(), client))
			defer server.Close()
			server.Client().Timeout = 2 * time.Second
			response, err := server.Client().Get(server.URL + clusterOpsTestLogs)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			events, raw := clusterOpsReadEvents(t, response)
			if response.StatusCode != 200 || len(events) != 2 || events[0].name != "ready" || events[1].name != tc.event || strings.Contains(raw, "sentinel") {
				t.Fatalf("wrong terminal sequence: %s", raw)
			}
			var terminal struct {
				Reason string `json:"reason"`
			}
			if json.Unmarshal(events[1].data, &terminal) != nil || terminal.Reason != tc.reason {
				t.Fatalf("wrong terminal payload: %s", events[1].data)
			}
			clusterOpsWaitCanceled(t, clusterOpsWaitValue(t, opened))
		})
	}
}

func TestClusterOpsLogsCallerCancellationReleasesDownstream(t *testing.T) {
	opened := make(chan context.Context, 1)
	stopped := make(chan struct{})
	client := &clusterOpsLogClientStub{open: func(ctx context.Context, _ string, _ *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
		opened <- ctx
		return &clusterOpsScriptedStream{ctx: ctx, frames: []*clusteropsv1.PodLogFrame{clusterOpsReadyFrame()}, stopped: stopped}, nil
	}}
	server := httptest.NewServer(clusterOpsLogServer(clusterOpsLogPrincipal(), client))
	defer server.Close()
	server.Client().Timeout = 2 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+clusterOpsTestLogs, nil)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "event: ready\n" {
		t.Fatalf("READY was not flushed before waiting: %q %v", line, err)
	}
	cancel()
	_ = response.Body.Close()
	clusterOpsWaitCanceled(t, clusterOpsWaitValue(t, opened))
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("blocked downstream Recv did not return on cancellation")
	}
}

type clusterOpsDeadlineResponse struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
	write     func([]byte) (int, error)
}

func (w *clusterOpsDeadlineResponse) SetWriteDeadline(value time.Time) error {
	w.deadlines = append(w.deadlines, value)
	return nil
}
func (w *clusterOpsDeadlineResponse) Write(data []byte) (int, error) {
	if w.write != nil {
		return w.write(data)
	}
	return w.ResponseRecorder.Write(data)
}
func (w *clusterOpsDeadlineResponse) WriteString(data string) (int, error) {
	return w.Write([]byte(data))
}

func TestClusterOpsLogsRequireBoundedTransportAndCancelOnWriteFailure(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "unsupported deadline", true: "failed bounded write"}[supported], func(t *testing.T) {
			opened := make(chan context.Context, 1)
			client := &clusterOpsLogClientStub{open: func(ctx context.Context, _ string, _ *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
				opened <- ctx
				return &clusterOpsScriptedStream{ctx: ctx, frames: []*clusteropsv1.PodLogFrame{clusterOpsReadyFrame()}}, nil
			}}
			recorder := httptest.NewRecorder()
			var response http.ResponseWriter = recorder
			bounded := &clusterOpsDeadlineResponse{ResponseRecorder: recorder, write: func([]byte) (int, error) { return 0, io.ErrClosedPipe }}
			if supported {
				response = bounded
			}
			started := time.Now()
			clusterOpsLogServer(clusterOpsLogPrincipal(), client).ServeHTTP(response, httptest.NewRequest("GET", clusterOpsTestLogs, nil))
			if supported {
				if recorder.Code != 200 || recorder.Body.Len() != 0 || len(bounded.deadlines) < 3 || !bounded.deadlines[len(bounded.deadlines)-1].IsZero() {
					t.Fatalf("write failure appended JSON or omitted deadline cleanup: status=%d body=%s deadlines=%v", recorder.Code, recorder.Body.String(), bounded.deadlines)
				}
				for _, deadline := range bounded.deadlines[:len(bounded.deadlines)-1] {
					if remaining := deadline.Sub(started); remaining < 4*time.Second || remaining > 6*time.Second {
						t.Fatalf("write was not bounded to five seconds: %v", remaining)
					}
				}
			} else {
				var body clusterOpsErrorBody
				if json.Unmarshal(recorder.Body.Bytes(), &body) != nil || recorder.Code != 503 || body.Reason != "CLUSTEROPS_UNAVAILABLE" {
					t.Fatalf("unsupported transport committed SSE: status=%d body=%s", recorder.Code, recorder.Body.String())
				}
			}
			clusterOpsWaitCanceled(t, clusterOpsWaitValue(t, opened))
		})
	}
}

type clusterOpsBackpressureStream struct {
	grpc.ClientStream
	ctx      context.Context
	received atomic.Int32
	overrun  chan struct{}
	once     sync.Once
}

func (s *clusterOpsBackpressureStream) Context() context.Context { return s.ctx }
func (s *clusterOpsBackpressureStream) Recv() (*clusteropsv1.PodLogFrame, error) {
	n := s.received.Add(1)
	if n == 1 {
		return clusterOpsReadyFrame(), nil
	}
	if n > 3 {
		s.once.Do(func() { close(s.overrun) })
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	return &clusteropsv1.PodLogFrame{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_DATA, Data: []byte("line\n")}, nil
}

func TestClusterOpsLogsReceiveAheadStaysBoundedWhileWriterBlocks(t *testing.T) {
	opened := make(chan *clusterOpsBackpressureStream, 1)
	client := &clusterOpsLogClientStub{open: func(ctx context.Context, _ string, _ *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
		stream := &clusterOpsBackpressureStream{ctx: ctx, overrun: make(chan struct{})}
		opened <- stream
		return stream, nil
	}}
	blocked, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	response := &clusterOpsDeadlineResponse{ResponseRecorder: httptest.NewRecorder(), write: func([]byte) (int, error) {
		once.Do(func() { close(blocked) })
		<-release
		return 0, io.ErrClosedPipe
	}}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		clusterOpsLogServer(clusterOpsLogPrincipal(), client).ServeHTTP(response, httptest.NewRequest("GET", clusterOpsTestLogs, nil))
	}()
	stream := clusterOpsWaitValue(t, opened)
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("writer did not reach the blocked boundary")
	}
	select {
	case <-stream.overrun:
		t.Error("receiver accumulated more than one queued frame plus one in-flight Recv")
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not release after the bounded writer failed")
	}
	clusterOpsWaitCanceled(t, stream.ctx)
}

func TestClusterOpsLogsOpeningUsesCallerDeadlineBeforeReady(t *testing.T) {
	opened := make(chan context.Context, 1)
	client := &clusterOpsLogClientStub{open: func(ctx context.Context, _ string, _ *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
		opened <- ctx
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	shortCaller := func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			call, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
			defer cancel()
			return next(call, req)
		}
	}
	server := httptest.NewServer(clusterOpsLogServer(clusterOpsLogPrincipal(), client, shortCaller))
	defer server.Close()
	server.Client().Timeout = 2 * time.Second
	response, err := server.Client().Get(server.URL + clusterOpsTestLogs)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var body clusterOpsErrorBody
	if json.Unmarshal(raw, &body) != nil || response.StatusCode != 504 || body.Reason != "DEADLINE_EXCEEDED" || strings.Contains(response.Header.Get("Content-Type"), "event-stream") {
		t.Fatalf("READY wait ignored caller deadline or committed SSE: status=%d body=%s", response.StatusCode, raw)
	}
	clusterOpsWaitCanceled(t, clusterOpsWaitValue(t, opened))
}

type clusterOpsBusyStream struct {
	grpc.ClientStream
	ctx      context.Context
	data     []byte
	received atomic.Int32
}

func (s *clusterOpsBusyStream) Context() context.Context { return s.ctx }
func (s *clusterOpsBusyStream) Recv() (*clusteropsv1.PodLogFrame, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	if s.received.Add(1) == 1 {
		return clusterOpsReadyFrame(), nil
	}
	return &clusteropsv1.PodLogFrame{Type: clusteropsv1.PodLogFrameType_POD_LOG_FRAME_TYPE_DATA, Data: s.data}, nil
}

func TestClusterOpsLogsSlowTCPReaderHasFiniteWriteExit(t *testing.T) {
	opened := make(chan *clusterOpsBusyStream, 1)
	client := &clusterOpsLogClientStub{open: func(ctx context.Context, _ string, _ *clusteropsv1.StreamPodLogsRequest) (grpc.ServerStreamingClient[clusteropsv1.PodLogFrame], error) {
		stream := &clusterOpsBusyStream{ctx: ctx, data: make([]byte, 16<<10)}
		opened <- stream
		return stream, nil
	}}
	server := httptest.NewServer(clusterOpsLogServer(clusterOpsLogPrincipal(), client))
	defer server.Close()
	// Keep the client alive without consuming its body. The server's five-second
	// socket deadline, rather than a client timeout/cancel, must release Res.
	server.Client().Timeout = 0
	transport := server.Client().Transport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 2 * time.Second
	server.Client().Transport = transport
	defer transport.CloseIdleConnections()
	response, err := server.Client().Get(server.URL + clusterOpsTestLogs)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	stream := clusterOpsWaitValue(t, opened)
	select {
	case <-stream.ctx.Done():
	case <-time.After(8 * time.Second):
		t.Fatal("slow TCP reader retained the downstream log stream beyond its write budget")
	}
	if response.StatusCode != 200 || stream.received.Load() < 4 {
		t.Fatalf("slow-reader scenario did not establish a real byte stream: status=%d frames=%d", response.StatusCode, stream.received.Load())
	}
}
