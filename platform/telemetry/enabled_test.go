package telemetry_test

import (
	"context"
	"slices"
	"testing"
	"time"

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
		Endpoint:    "127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

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

	if _, ok := otel.GetMeterProvider().(*sdkmetric.MeterProvider); !ok {
		t.Errorf("глобальный MeterProvider = %T, want *sdkmetric.MeterProvider", otel.GetMeterProvider())
	}
}

//nolint:paralleltest // регистрирует глобальные провайдеры OTel и/или меняет env, тест нельзя запускать параллельно
func TestNew_Enabled_RegistersTracerProviderAndPropagator(t *testing.T) {
	newEnabled(t)

	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Errorf("глобальный TracerProvider = %T, want *sdktrace.TracerProvider", otel.GetTracerProvider())
	}

	fields := otel.GetTextMapPropagator().Fields()
	for _, want := range []string{"traceparent", "baggage"} {
		if !slices.Contains(fields, want) {
			t.Errorf("пропагатор не содержит поле %q: %v", want, fields)
		}
	}
}

func TestNew_Enabled_Sampler(t *testing.T) {
	tests := []struct {
		name    string
		sampler string // значение OTEL_TRACES_SAMPLER, "" — не задан
		want    bool
	}{
		{name: "по умолчанию ParentBased(AlwaysSample)", sampler: "", want: true},
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

			if got := span.SpanContext().IsSampled(); got != tt.want {
				t.Errorf("IsSampled() = %v, want %v", got, tt.want)
			}
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

	if !child.SpanContext().IsSampled() {
		t.Error("дочерний спан сэмплируемого родителя не сэмплируется")
	}
}
