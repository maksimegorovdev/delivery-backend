package logger_test

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.Len(t, records, 1, "otel records")

	got := records[0]
	assert.Equal(t, testTraceID, got.TraceID().String())
	assert.Equal(t, testSpanID, got.SpanID().String())
	assert.Equal(t, log.SeverityWarn, got.Severity())
	assert.Equal(t, "hello", got.Body().String())

	a := attrs(got)
	assert.Equal(t, "req-1", a["request_id"], "attrs=%v", a)
	assert.NotContains(t, a, "trace_id", "trace_id must not be duplicated in otel attributes")
	assert.NotContains(t, a, "span_id", "span_id must not be duplicated in otel attributes")
	assert.Equal(t, "GET", a["http.method"], "group http must be expanded to http.*, attrs=%v", a)
	assert.Equal(t, "200", a["http.status"], "group http must be expanded to http.*, attrs=%v", a)

	// stdout по-прежнему получает trace_id/span_id от traceHandler.
	out := stdout.String()
	for _, want := range []string{"trace_id=" + testTraceID, "span_id=" + testSpanID, "request_id=req-1"} {
		assert.Contains(t, out, want)
	}
}

func TestWithLoggerProvider_WithAttrsAndGroupReachOTel(t *testing.T) {
	t.Parallel()

	rec, provider := newRecorder(t)

	l := logger.New(logger.WithWriter(&bytes.Buffer{}), logger.WithLoggerProvider(provider)).
		With(slog.String("app_name", "gateway"))

	l.Info("started")

	records := rec.snapshot()
	require.Len(t, records, 1, "otel records")
	assert.Equal(t, "gateway", attrs(records[0])["app_name"])
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

			assert.Equal(t, tt.wantEmits, stdout.Len() > 0, "stdout written")
			assert.Equal(t, tt.wantEmits, len(rec.snapshot()) > 0, "otel written")
		})
	}
}

func TestWithLoggerProvider_NilKeepsStdoutOnly(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer

	l := logger.New(logger.WithWriter(&stdout), logger.WithLoggerProvider(nil))
	l.Info("hello")

	require.NotEmpty(t, stdout.String(), "stdout is empty")
}

func TestWithLoggerProvider_FlattensGroups(t *testing.T) {
	t.Parallel()

	rec, provider := newRecorder(t)

	l := logger.New(logger.WithWriter(&bytes.Buffer{}), logger.WithLoggerProvider(provider)).
		WithGroup("req").
		With(slog.Group("user", slog.String("id", "u1")))

	l.Info("hello", slog.Group("http", slog.Group("route", slog.String("path", "/orders"))), slog.Group("empty"))

	records := rec.snapshot()
	require.Len(t, records, 1, "otel records")

	want := map[string]string{
		"req.user.id":         "u1",
		"req.http.route.path": "/orders",
	}
	assert.Equal(t, want, attrs(records[0]))
}
