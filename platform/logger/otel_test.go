package logger_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
)

// recorder собирает записи, дошедшие до OTel-провайдера.
type recorder struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (r *recorder) Export(_ context.Context, records []sdklog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, rec := range records {
		r.records = append(r.records, rec.Clone())
	}

	return nil
}

func (*recorder) Shutdown(context.Context) error   { return nil }
func (*recorder) ForceFlush(context.Context) error { return nil }

func (r *recorder) snapshot() []sdklog.Record {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]sdklog.Record(nil), r.records...)
}

func newRecorder(t *testing.T) (*recorder, *sdklog.LoggerProvider) {
	t.Helper()

	rec := &recorder{}
	provider := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(rec)))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	return rec, provider
}

func attrs(rec sdklog.Record) map[string]string {
	got := make(map[string]string)
	rec.WalkAttributes(func(kv attribute.KeyValue) bool {
		got[string(kv.Key)] = kv.Value.String()

		return true
	})

	return got
}

func TestWithLoggerProvider_RecordFields(t *testing.T) {
	t.Parallel()

	rec, provider := newRecorder(t)
	var stdout bytes.Buffer

	l := logger.New(logger.WithWriter(&stdout), logger.WithLoggerProvider(provider))
	ctx := appctx.WithRequestID(spanContext(t), "req-1")

	l.WarnContext(ctx, "hello", slog.Group("http", slog.String("method", "GET"), slog.Int("status", 200)))

	records := rec.snapshot()
	if len(records) != 1 {
		t.Fatalf("otel records = %d, want 1", len(records))
	}

	got := records[0]
	if id := got.TraceID().String(); id != testTraceID {
		t.Errorf("TraceID = %s, want %s", id, testTraceID)
	}
	if id := got.SpanID().String(); id != testSpanID {
		t.Errorf("SpanID = %s, want %s", id, testSpanID)
	}
	if got.Severity() != log.SeverityWarn {
		t.Errorf("Severity = %v, want %v", got.Severity(), log.SeverityWarn)
	}
	if got.Body().String() != "hello" {
		t.Errorf("Body = %q, want hello", got.Body().String())
	}

	a := attrs(got)
	if a["request_id"] != "req-1" {
		t.Errorf("request_id = %q, want req-1; attrs=%v", a["request_id"], a)
	}
	if _, ok := a["trace_id"]; ok {
		t.Errorf("trace_id must not be duplicated in otel attributes: %v", a)
	}
	if _, ok := a["span_id"]; ok {
		t.Errorf("span_id must not be duplicated in otel attributes: %v", a)
	}
	if a["http.method"] != "GET" || a["http.status"] != "200" {
		t.Errorf("group http must be expanded to http.*, attrs=%v", a)
	}

	// stdout по-прежнему получает trace_id/span_id от traceHandler.
	out := stdout.String()
	for _, want := range []string{"trace_id=" + testTraceID, "span_id=" + testSpanID, "request_id=req-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout %q misses %q", out, want)
		}
	}
}

func TestWithLoggerProvider_WithAttrsAndGroupReachOTel(t *testing.T) {
	t.Parallel()

	rec, provider := newRecorder(t)

	l := logger.New(logger.WithWriter(&bytes.Buffer{}), logger.WithLoggerProvider(provider)).
		With(slog.String("app_name", "gateway"))

	l.Info("started")

	records := rec.snapshot()
	if len(records) != 1 {
		t.Fatalf("otel records = %d, want 1", len(records))
	}
	if got := attrs(records[0])["app_name"]; got != "gateway" {
		t.Errorf("app_name = %q, want gateway", got)
	}
}

func TestWithLoggerProvider_LevelAppliedBeforeFanOut(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		level     string
		emit      func(*slog.Logger)
		wantEmits bool
	}{
		{"debug below info", "info", func(l *slog.Logger) { l.Debug("x") }, false},
		{"info below warn", "warn", func(l *slog.Logger) { l.Info("x") }, false},
		{"warn below error", "error", func(l *slog.Logger) { l.Warn("x") }, false},
		{"info at info", "info", func(l *slog.Logger) { l.Info("x") }, true},
		{"error at warn", "warn", func(l *slog.Logger) { l.Error("x") }, true},
		{"debug at debug", "debug", func(l *slog.Logger) { l.Debug("x") }, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec, provider := newRecorder(t)
			var stdout bytes.Buffer

			l := logger.New(
				logger.WithWriter(&stdout),
				logger.WithLevel(tt.level),
				logger.WithLoggerProvider(provider),
			)
			tt.emit(l)

			if got := stdout.Len() > 0; got != tt.wantEmits {
				t.Errorf("stdout written = %v, want %v", got, tt.wantEmits)
			}
			if got := len(rec.snapshot()) > 0; got != tt.wantEmits {
				t.Errorf("otel written = %v, want %v", got, tt.wantEmits)
			}
		})
	}
}

func TestWithLoggerProvider_NilKeepsStdoutOnly(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	l := logger.New(logger.WithWriter(&stdout), logger.WithLoggerProvider(nil))
	l.Info("hello")

	if stdout.Len() == 0 {
		t.Fatal("stdout is empty")
	}
}

func TestWithLoggerProvider_FlattensGroups(t *testing.T) {
	t.Parallel()

	rec, provider := newRecorder(t)

	l := logger.New(logger.WithWriter(&bytes.Buffer{}), logger.WithLoggerProvider(provider)).
		WithGroup("req").
		With(slog.Group("user", slog.String("id", "u1")))

	l.Info("hello", slog.Group("http", slog.Group("route", slog.String("path", "/orders"))), slog.Group("empty"))

	records := rec.snapshot()
	if len(records) != 1 {
		t.Fatalf("otel records = %d, want 1", len(records))
	}

	a := attrs(records[0])
	want := map[string]string{
		"req.user.id":         "u1",
		"req.http.route.path": "/orders",
	}
	for k, v := range want {
		if a[k] != v {
			t.Errorf("attr %s = %q, want %q; attrs=%v", k, a[k], v, a)
		}
	}
	if len(a) != len(want) {
		t.Errorf("attrs = %v, want exactly %v", a, want)
	}
}
