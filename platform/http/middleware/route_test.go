package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
)

func newRouter() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Route)
	r.Get("/orders/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return r
}

// serveRouted прогоняет запрос через chi с активным спаном и Labeler в контексте,
// как это делает otelhttp.
func serveRouted(t *testing.T, handler http.Handler, path string) (*tracetest.InMemoryExporter, *otelhttp.Labeler) {
	t.Helper()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	labeler := &otelhttp.Labeler{}

	ctx, span := tp.Tracer("test").Start(t.Context(), "request")
	ctx = otelhttp.ContextWithLabeler(ctx, labeler)
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	span.End()

	return exp, labeler
}

func TestRoute_SetsPatternOnSpanAndLabeler(t *testing.T) {
	t.Parallel()

	exp, labeler := serveRouted(t, newRouter(), "/orders/123")

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /orders/{id}", spans[0].Name)
	route := attribute.String("http.route", "/orders/{id}")
	assert.Contains(t, spans[0].Attributes, route)
	assert.Contains(t, labeler.Get(), route)
}

func TestRoute_SkipsUnmatchedRoute(t *testing.T) {
	t.Parallel()

	exp, labeler := serveRouted(t, newRouter(), "/unknown")

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "request", spans[0].Name)
	assert.Empty(t, spans[0].Attributes)
	assert.Empty(t, labeler.Get())
}

func TestRoute_SkipsRequestWithoutChiContext(t *testing.T) {
	t.Parallel()

	handler := middleware.Route(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	rec := httptest.NewRecorder()

	require.NotPanics(t, func() {
		handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	})
	assert.Equal(t, http.StatusAccepted, rec.Code)
}

func TestRoute_WorksWithoutLabeler(t *testing.T) {
	t.Parallel()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))

	ctx, span := tp.Tracer("test").Start(t.Context(), "request")
	newRouter().ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/orders/1", nil))
	span.End()

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /orders/{id}", spans[0].Name)
}
