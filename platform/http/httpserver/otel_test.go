package httpserver_test

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/maksimegorovdev/delivery-backend/platform/http/httpserver"
)

const (
	requestDuration = "http.server.request.duration"
	parentSpanID    = "00f067aa0ba902b7"
)

//nolint:gochecknoglobals // глобальные провайдеры OTel ставятся один раз в TestMain
var (
	spans   = tracetest.NewInMemoryExporter()
	metrics = sdkmetric.NewManualReader()
)

// Глобальные провайдеры ставятся один раз: otelhttp берёт их при создании
// хендлера через делегирующий глобальный провайдер.
func TestMain(m *testing.M) {
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans)))
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(metrics)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	os.Exit(m.Run())
}

// serve выполняет GET target через хендлер сервера с маршрутом route, отвечающим status.
// Запрос приходит с уникальным traceparent: по его TraceId тест отбирает свои спаны,
// поэтому тесты не мешают друг другу и идут параллельно.
func serve(t *testing.T, status int, route, target string) string {
	t.Helper()

	raw := make([]byte, 16)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	traceID := hex.EncodeToString(raw)

	srv := httpserver.New(":0")
	srv.Router().Get(route, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	req.Header.Set("Traceparent", "00-"+traceID+"-"+parentSpanID+"-01")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), req)

	return traceID
}

func spansOf(traceID string) tracetest.SpanStubs {
	var out tracetest.SpanStubs
	for _, s := range spans.GetSpans() {
		if s.SpanContext.TraceID().String() == traceID {
			out = append(out, s)
		}
	}

	return out
}

func TestServer_SpanNamedByRoutePattern(t *testing.T) {
	t.Parallel()

	got := spansOf(serve(t, http.StatusOK, "/api/v1/orders/{id}", "/api/v1/orders/42"))

	require.Len(t, got, 1)
	require.Equal(t, "GET /api/v1/orders/{id}", got[0].Name)
	require.Contains(t, got[0].Attributes, attribute.String("http.route", "/api/v1/orders/{id}"))
}

func TestServer_MetricHasRouteTemplate(t *testing.T) {
	t.Parallel()

	const route = "/api/v1/metric-orders/{id}"
	serve(t, http.StatusOK, route, "/api/v1/metric-orders/42")

	var rm metricdata.ResourceMetrics
	require.NoError(t, metrics.Collect(t.Context(), &rm))

	var routes []string
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != requestDuration {
				continue
			}
			hist, ok := m.Data.(metricdata.Histogram[float64])
			require.True(t, ok)
			for _, dp := range hist.DataPoints {
				if v, found := dp.Attributes.Value("http.route"); found {
					routes = append(routes, v.AsString())
				}
			}
		}
	}

	require.Contains(t, routes, route)
	require.NotContains(t, routes, "/api/v1/metric-orders/42")
}

func TestServer_SwaggerNotTraced(t *testing.T) {
	t.Parallel()

	require.Empty(t, spansOf(serve(t, http.StatusOK, "/swagger/*", "/swagger/index.html")))
}

func TestServer_SpanStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   codes.Code
	}{
		{name: "500 помечает спан ошибкой", status: http.StatusInternalServerError, want: codes.Error},
		{name: "404 не ошибка на сервере", status: http.StatusNotFound, want: codes.Unset},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := spansOf(serve(t, tt.status, "/api/v1/orders/{id}", "/api/v1/orders/42"))

			require.Len(t, got, 1)
			require.Equal(t, tt.want, got[0].Status.Code)
		})
	}
}

func TestServer_ContinuesIncomingTrace(t *testing.T) {
	t.Parallel()

	got := spansOf(serve(t, http.StatusOK, "/api/v1/orders/{id}", "/api/v1/orders/42"))

	require.Len(t, got, 1)
	require.Equal(t, parentSpanID, got[0].Parent.SpanID().String())
}
