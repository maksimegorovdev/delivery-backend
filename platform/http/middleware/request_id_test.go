package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"uuid"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
)

func serveWithSpan(t *testing.T, requestID string) (*tracetest.InMemoryExporter, *httptest.ResponseRecorder) {
	t.Helper()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))

	handler := middleware.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))

	ctx, span := tp.Tracer("test").Start(t.Context(), "request")
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if requestID != "" {
		req.Header.Set(middleware.RequestIDHeader, requestID)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	span.End()

	return exp, rec
}

func TestRequestID_AddsAttributeToActiveSpan(t *testing.T) {
	t.Parallel()

	exp, _ := serveWithSpan(t, "req-42")

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	require.Contains(t, spans[0].Attributes, attribute.String("request.id", "req-42"))
}

func TestRequestID_GeneratedIDMatchesResponseHeader(t *testing.T) {
	t.Parallel()

	exp, rec := serveWithSpan(t, "")

	id := rec.Header().Get(middleware.RequestIDHeader)
	require.NotEmpty(t, id)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	require.Contains(t, spans[0].Attributes, attribute.String("request.id", id))
}

func TestRequestID_KeepsValidIncomingID(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"uuid":       "0192f5e4-7c1a-7b3e-9d2f-3a4b5c6d7e8f",
		"underscore": "req_42",
		"max length": strings.Repeat("a", 128),
		"mixed case": "AbC-123",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, rec := serveWithSpan(t, id)
			require.Equal(t, id, rec.Header().Get(middleware.RequestIDHeader))
		})
	}
}

func TestRequestID_ReplacesInvalidIncomingID(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"too long":  strings.Repeat("a", 129),
		"space":     "req 42",
		"newline":   "req\n42",
		"quote":     `req"42`,
		"non-ascii": "запрос",
	}
	for name, id := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			exp, rec := serveWithSpan(t, id)

			got := rec.Header().Get(middleware.RequestIDHeader)
			require.NotEqual(t, id, got)
			_, err := uuid.Parse(got)
			require.NoError(t, err)

			spans := exp.GetSpans()
			require.Len(t, spans, 1)
			require.Contains(t, spans[0].Attributes, attribute.String("request.id", got))
		})
	}
}
