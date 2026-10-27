package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
