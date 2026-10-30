package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/maksimegorovdev/delivery-backend/platform/telemetry"
)

// newEnabled создаёт Telemetry с Enabled=true и возвращает глобальное состояние OTel
// к исходному после теста. Collector не нужен: gRPC-соединение ленивое.
// Глобальный LoggerProvider не восстанавливается: повторная установка дефолтного
// делегата otel пишет предупреждение, а следующий newEnabled всё равно перезапишет его.
func newEnabled(t *testing.T) *telemetry.Telemetry {
	t.Helper()

	prevTracer := otel.GetTracerProvider()
	prevMeter := otel.GetMeterProvider()
	prevProp := otel.GetTextMapPropagator()

	tel, err := telemetry.New(context.Background(), telemetry.Config{
		Enabled:     true,
		ServiceName: "test-service",
		Endpoint:    "http://127.0.0.1:1",
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		_ = tel.Shutdown(ctx)
		otel.SetTracerProvider(prevTracer)
		otel.SetMeterProvider(prevMeter)
		otel.SetTextMapPropagator(prevProp)
	})
	return tel
}

//nolint:paralleltest // регистрирует глобальные провайдеры OTel и/или меняет env, тест нельзя запускать параллельно
func TestNew_Enabled_RegistersMeterProvider(t *testing.T) {
	newEnabled(t)

	assert.IsType(t, &sdkmetric.MeterProvider{}, otel.GetMeterProvider())
}

//nolint:paralleltest // регистрирует глобальные провайдеры OTel и/или меняет env, тест нельзя запускать параллельно
func TestNew_Enabled_RegistersTracerProviderAndPropagator(t *testing.T) {
	newEnabled(t)

	assert.IsType(t, &sdktrace.TracerProvider{}, otel.GetTracerProvider())
	assert.Subset(t, otel.GetTextMapPropagator().Fields(), []string{"traceparent", "baggage"})
}

func TestNew_Enabled_Sampler(t *testing.T) {
	tests := []struct {
		name    string
		sampler string // значение OTEL_TRACES_SAMPLER, "" — не задан
		want    bool
	}{
		{name: "default ParentBased(AlwaysSample)", sampler: "", want: true},
		{name: "OTEL_TRACES_SAMPLER=always_off", sampler: "always_off", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.sampler != "" {
				t.Setenv("OTEL_TRACES_SAMPLER", tt.sampler)
			}
			newEnabled(t)

			_, span := otel.Tracer("test").Start(context.Background(), "op")
			defer span.End()

			assert.Equal(t, tt.want, span.SpanContext().IsSampled())
		})
	}
}

//nolint:paralleltest // регистрирует глобальные провайдеры OTel и/или меняет env, тест нельзя запускать параллельно
func TestNew_Enabled_ChildFollowsParentSampling(t *testing.T) {
	newEnabled(t)

	tracer := otel.Tracer("test")
	ctx, parent := tracer.Start(context.Background(), "parent")
	defer parent.End()
	_, child := tracer.Start(ctx, "child")
	defer child.End()

	assert.True(t, child.SpanContext().IsSampled(), "child of a sampled parent must be sampled")
}
