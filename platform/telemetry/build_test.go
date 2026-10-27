//nolint:testpackage // тест использует неэкспортируемые символы пакета
package telemetry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Если создание провайдера логов падает, уже созданные провайдеры трейсов и
// метрик закрываются, а глобальное состояние OTel остаётся нетронутым.
//
//nolint:paralleltest // регистрирует глобальные провайдеры OTel, тест нельзя запускать параллельно
func TestBuild_LoggerFailure_ShutsDownCreatedProviders(t *testing.T) {
	var (
		tp *sdktrace.TracerProvider
		mp *sdkmetric.MeterProvider
	)
	failure := errors.New("logger boom")

	f := defaultFactories()
	f.tracer = func(ctx context.Context, cfg Config, res *resource.Resource) (*sdktrace.TracerProvider, error) {
		var err error
		tp, err = defaultFactories().tracer(ctx, cfg, res)
		return tp, err
	}
	f.meter = func(ctx context.Context, cfg Config, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
		var err error
		mp, err = defaultFactories().meter(ctx, cfg, res)
		return mp, err
	}
	f.logger = func(context.Context, Config, *resource.Resource) (*sdklog.LoggerProvider, error) {
		return nil, failure
	}

	tracerBefore := otel.GetTracerProvider()
	meterBefore := otel.GetMeterProvider()

	cfg := Config{Enabled: true, ServiceName: "test-service", Endpoint: "http://127.0.0.1:1"}
	tel, err := build(context.Background(), cfg, f)
	if !errors.Is(err, failure) {
		t.Fatalf("build: err = %v, want %v", err, failure)
	}
	if tel != nil {
		t.Errorf("build вернул %v вместе с ошибкой", tel)
	}

	if otel.GetTracerProvider() != tracerBefore {
		t.Error("TracerProvider изменился после неудачного build")
	}
	if otel.GetMeterProvider() != meterBefore {
		t.Error("MeterProvider изменился после неудачного build")
	}

	_, span := tp.Tracer("test").Start(context.Background(), "op")
	if span.IsRecording() {
		t.Error("TracerProvider не закрыт после неудачного build")
	}
	// Повторный Shutdown закрытого MeterProvider возвращает ошибку reader'а.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if shutdownErr := mp.Shutdown(ctx); shutdownErr == nil || !strings.Contains(shutdownErr.Error(), "shutdown") {
		t.Errorf("MeterProvider не закрыт после неудачного build: повторный Shutdown = %v", shutdownErr)
	}
}
