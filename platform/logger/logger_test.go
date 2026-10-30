package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
)

const (
	testTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpanID  = "00f067aa0ba902b7"
)

var timeAttr = regexp.MustCompile(`time=\S+ `)

func spanContext(t *testing.T) context.Context {
	t.Helper()

	traceID, err := trace.TraceIDFromHex(testTraceID)
	require.NoError(t, err, "trace id")
	spanID, err := trace.SpanIDFromHex(testSpanID)
	require.NoError(t, err, "span id")

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
	})

	return trace.ContextWithSpanContext(context.Background(), sc)
}

func TestTraceHandler_Text(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  func(t *testing.T) context.Context
		want string
	}{
		{
			name: "valid span context adds trace_id and span_id",
			ctx:  spanContext,
			want: "level=INFO msg=hello trace_id=" + testTraceID + " span_id=" + testSpanID + "\n",
		},
		{
			name: "no span keeps the original format",
			ctx:  func(*testing.T) context.Context { return context.Background() },
			want: "level=INFO msg=hello\n",
		},
		{
			name: "request_id stays and goes before trace ids",
			ctx: func(t *testing.T) context.Context {
				t.Helper()

				return appctx.WithRequestID(spanContext(t), "req-1")
			},
			want: "level=INFO msg=hello request_id=req-1 trace_id=" + testTraceID + " span_id=" + testSpanID + "\n",
		},
		{
			name: "request_id without span keeps the original format",
			ctx: func(*testing.T) context.Context {
				return appctx.WithRequestID(context.Background(), "req-1")
			},
			want: "level=INFO msg=hello request_id=req-1\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			log := logger.New(logger.WithWriter(&buf))

			log.InfoContext(tt.ctx(t), "hello")

			got := timeAttr.ReplaceAllString(buf.String(), "")
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTraceHandler_JSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  func(t *testing.T) context.Context
		want map[string]any
	}{
		{
			name: "valid span context adds trace_id and span_id",
			ctx:  spanContext,
			want: map[string]any{
				"level": "INFO", "msg": "hello",
				"trace_id": testTraceID, "span_id": testSpanID,
			},
		},
		{
			name: "no span keeps the original format",
			ctx:  func(*testing.T) context.Context { return context.Background() },
			want: map[string]any{"level": "INFO", "msg": "hello"},
		},
		{
			name: "request_id stays next to trace ids",
			ctx: func(t *testing.T) context.Context {
				t.Helper()

				return appctx.WithRequestID(spanContext(t), "req-1")
			},
			want: map[string]any{
				"level": "INFO", "msg": "hello", "request_id": "req-1",
				"trace_id": testTraceID, "span_id": testSpanID,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			log := logger.New(logger.WithWriter(&buf), logger.WithFormat("json"))

			log.InfoContext(tt.ctx(t), "hello")

			var got map[string]any
			require.NoError(t, json.Unmarshal(buf.Bytes(), &got), "invalid json %q", buf.String())
			delete(got, "time")

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTraceHandler_JSONGoldenWithoutSpan(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logger.New(logger.WithWriter(&buf), logger.WithFormat("json"))

	log.InfoContext(context.Background(), "hello", "k", "v")

	got := regexp.MustCompile(`"time":"[^"]+",`).ReplaceAllString(buf.String(), "")
	want := `{"level":"INFO","msg":"hello","k":"v"}` + "\n"
	assert.Equal(t, want, got)
}

func TestTraceHandler_WithAttrsKeepsWrapper(t *testing.T) {
	t.Parallel()

	t.Run("With keeps trace ids", func(t *testing.T) {
		t.Parallel()

		var buf bytes.Buffer
		log := logger.New(logger.WithWriter(&buf)).With("svc", "gateway")

		log.InfoContext(spanContext(t), "hello")

		out := buf.String()
		for _, want := range []string{"svc=gateway", "trace_id=" + testTraceID, "span_id=" + testSpanID} {
			assert.Contains(t, out, want)
		}
	})
}

func TestLogger_WithGroupKeepsCtxAttrsAtTopLevel(t *testing.T) {
	t.Parallel()

	const ctxAttrs = " request_id=req-1 trace_id=" + testTraceID + " span_id=" + testSpanID + "\n"

	tests := []struct {
		name string
		log  func(*slog.Logger) *slog.Logger
		args []any
		want string
	}{
		{
			name: "empty group is omitted",
			log:  func(l *slog.Logger) *slog.Logger { return l.WithGroup("http") },
			want: "level=INFO msg=hello" + ctxAttrs,
		},
		{
			name: "record attrs go into the group",
			log:  func(l *slog.Logger) *slog.Logger { return l.WithGroup("http") },
			args: []any{"k", "v"},
			want: "level=INFO msg=hello http.k=v" + ctxAttrs,
		},
		{
			name: "With before and after the group",
			log: func(l *slog.Logger) *slog.Logger {
				return l.With("svc", "gateway").WithGroup("http").With("a", "1")
			},
			args: []any{"k", "v"},
			want: "level=INFO msg=hello svc=gateway http.a=1 http.k=v" + ctxAttrs,
		},
		{
			name: "nested groups",
			log: func(l *slog.Logger) *slog.Logger {
				return l.WithGroup("a").With("x", "1").WithGroup("b")
			},
			args: []any{"k", "v"},
			want: "level=INFO msg=hello a.x=1 a.b.k=v" + ctxAttrs,
		},
		{
			name: "empty group name is ignored",
			log:  func(l *slog.Logger) *slog.Logger { return l.WithGroup("") },
			args: []any{"k", "v"},
			want: "level=INFO msg=hello k=v" + ctxAttrs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			log := tt.log(logger.New(logger.WithWriter(&buf)))

			log.InfoContext(appctx.WithRequestID(spanContext(t), "req-1"), "hello", tt.args...)

			got := timeAttr.ReplaceAllString(buf.String(), "")
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLogger_WithGroupKeepsCtxAttrsAtTopLevelJSON(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	log := logger.New(logger.WithWriter(&buf), logger.WithFormat("json")).WithGroup("http")

	log.InfoContext(appctx.WithRequestID(spanContext(t), "req-1"), "hello", "k", "v")

	var got map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &got), "unmarshal %q", buf.String())

	for key, want := range map[string]string{
		"request_id": "req-1",
		"trace_id":   testTraceID,
		"span_id":    testSpanID,
	} {
		assert.Equal(t, want, got[key], "top-level %q", key)
	}

	assert.Equal(t, map[string]any{"k": "v"}, got["http"], `"http" group`)
}
