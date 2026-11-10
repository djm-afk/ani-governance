package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	conf "go-wind-admin/pkg/localdeps/kratos-bootstrap/api/gen/go/conf/v1"
	"go-wind-admin/pkg/telemetry"
)

func decodeLine(t *testing.T, line []byte) map[string]any {
	t.Helper()
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(line)))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("line is not JSON: %v\n%s", err, line)
	}
	return m
}

func TestNewRuntimeLoggerEmitsSingleLineJSON(t *testing.T) {
	var buf bytes.Buffer
	app := &conf.AppInfo{AppId: "ani-governance", Version: "test-1.0.0", InstanceId: "inst-1"}
	l, _ := newJSON(&buf, slog.LevelInfo)
	l = l.With(identityFields(app)...).(*stdLogger)

	l.Info(context.Background(), "hello", "event", "process.starting", "log_format", "json", "log_level", "INFO")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly one line, got %d: %q", len(lines), buf.String())
	}
	m := decodeLine(t, []byte(lines[0]))
	for _, key := range []string{"time", "level", "msg", "service.name", "service.version", "service.instance.id", "event"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing required field %q in %v", key, m)
		}
	}
	if m["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", m["level"])
	}
	if m["service.name"] != "ani-governance" {
		t.Errorf("service.name = %v", m["service.name"])
	}
	// Framework aliases must not leak into the record.
	for _, banned := range []string{"ts", "caller", "service.id", "service.instance"} {
		if _, ok := m[banned]; ok {
			t.Errorf("framework alias %q leaked into record", banned)
		}
	}
}

func TestBusinessEventDropsUnregisteredFields(t *testing.T) {
	var buf bytes.Buffer
	l, _ := newJSON(&buf, slog.LevelInfo)

	l.Info(context.Background(), "accepted", "event", "operation.accepted",
		"operation_id", "op-1", "action", "vm.create",
		"some_internal_debug", "should-not-appear")

	m := decodeLine(t, buf.Bytes())
	if _, ok := m["some_internal_debug"]; ok {
		t.Errorf("unregistered field leaked into business event: %v", m)
	}
	if m["operation_id"] != "op-1" {
		t.Errorf("operation_id = %v", m["operation_id"])
	}
}

func TestRuntimeLogKeepsFrameworkExtras(t *testing.T) {
	var buf bytes.Buffer
	l, _ := newJSON(&buf, slog.LevelInfo)

	// No explicit event: the handler defaults to runtime.log, which may carry
	// arbitrary framework attributes.
	l.Info(context.Background(), "framework chatter", "module", "bootstrap")

	m := decodeLine(t, buf.Bytes())
	if m["event"] != RuntimeEvent {
		t.Errorf("event = %v, want %s", m["event"], RuntimeEvent)
	}
	if m["module"] != "bootstrap" {
		t.Errorf("runtime.log dropped a framework attribute: %v", m)
	}
}

func TestCallerAliasBecomesSource(t *testing.T) {
	var buf bytes.Buffer
	l, _ := newJSON(&buf, slog.LevelInfo)

	l.Info(context.Background(), "x", "event", "process.ready", "caller", "handler.go:42")

	m := decodeLine(t, buf.Bytes())
	if m["source"] != "handler.go:42" {
		t.Errorf("source = %v, want handler.go:42", m["source"])
	}
	if _, ok := m["caller"]; ok {
		t.Errorf("caller alias must be mapped away")
	}
}

func TestBusinessAttrsAndTraceInjectedFromContext(t *testing.T) {
	var buf bytes.Buffer
	l, _ := newJSON(&buf, slog.LevelInfo)

	traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	ctx = telemetry.WithBusiness(ctx, telemetry.BusinessContext{OperationID: "op-9", TenantID: "t-1", Action: "vm.delete"})

	l.Info(ctx, "done", "event", "step.succeeded", "step", "delete_volume")

	m := decodeLine(t, buf.Bytes())
	if m["trace_id"] != traceID.String() {
		t.Errorf("trace_id = %v, want %s", m["trace_id"], traceID)
	}
	if m["span_id"] != spanID.String() {
		t.Errorf("span_id = %v, want %s", m["span_id"], spanID)
	}
	if m["operation_id"] != "op-9" || m["tenant_id"] != "t-1" {
		t.Errorf("business attrs not injected: %v", m)
	}
}

func TestSampledOutTraceStillLogs(t *testing.T) {
	var buf bytes.Buffer
	l, _ := newJSON(&buf, slog.LevelInfo)

	traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	// Sampled flag cleared == upstream sampled=00.
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	l.Error(ctx, "failed", "event", "step.failed", "step", "observe_volume",
		"error", "boom", "error_type", "local", "error_source", "local")

	if !strings.Contains(buf.String(), "step.failed") {
		t.Fatalf("record was NOT emitted for sampled-out trace: %q", buf.String())
	}
	m := decodeLine(t, buf.Bytes())
	if m["trace_id"] != traceID.String() {
		t.Errorf("trace_id must still be present for correlation")
	}
}

func TestParseRuntimeLevel(t *testing.T) {
	cases := []struct {
		raw     string
		want    slog.Level
		wantErr bool
	}{
		{"", slog.LevelInfo, false},
		{"info", slog.LevelInfo, false},
		{" INFO ", slog.LevelInfo, false},
		{"debug", slog.LevelDebug, false},
		{"DEBUG", slog.LevelDebug, false},
		{"warn", slog.LevelInfo, true},
		{"error", slog.LevelInfo, true},
		{"off", slog.LevelInfo, true},
		{"bogus", slog.LevelInfo, true},
	}
	for _, c := range cases {
		got, err := ParseRuntimeLevel(c.raw)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseRuntimeLevel(%q) err = %v, wantErr %v", c.raw, err, c.wantErr)
		}
		if got != c.want {
			t.Errorf("ParseRuntimeLevel(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

// LG02: switching to debug must add DEBUG records without dropping or
// downgrading the INFO/WARN/ERROR levels.
func TestDebugLevelShowsDebugAndKeepsInfo(t *testing.T) {
	var buf bytes.Buffer
	l, _ := newJSON(&buf, slog.LevelDebug)
	ctx := context.Background()
	l.Debug(ctx, "debug detail", "event", "runtime.log")
	l.Info(ctx, "info record", "event", "process.ready")
	l.Warn(ctx, "warn record", "event", "operation.conflict", "reason", "X")
	l.Error(ctx, "error record", "event", "step.failed", "step", "s",
		"error", "boom", "error_type", "local", "error_source", "local")

	levels := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		m := decodeLine(t, []byte(line))
		levels[m["level"].(string)] = true
	}
	for _, want := range []string{"DEBUG", "INFO", "WARN", "ERROR"} {
		if !levels[want] {
			t.Errorf("debug mode is missing %s records: %v", want, levels)
		}
	}
}

// LG03: an unsupported threshold must be reported, never silently applied.
func TestUnsupportedLevelReportsAndFallsBackToInfo(t *testing.T) {
	for _, raw := range []string{"warn", "error", "off", "verbose", "  WARN  "} {
		lv, err := ParseRuntimeLevel(raw)
		if err == nil {
			t.Errorf("ANI_LOG_LEVEL=%q must be rejected", raw)
		}
		if lv != slog.LevelInfo {
			t.Errorf("ANI_LOG_LEVEL=%q fell back to %v, want INFO (never a higher threshold)", raw, lv)
		}
	}
}
