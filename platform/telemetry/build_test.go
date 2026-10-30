//nolint:testpackage // тест использует неэкспортируемые символы пакета
package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.ErrorIs(t, err, failure)
	assert.Nil(t, tel, "build must not return Telemetry together with an error")

	assert.Same(t, tracerBefore, otel.GetTracerProvider(), "TracerProvider changed after failed build")
	assert.Same(t, meterBefore, otel.GetMeterProvider(), "MeterProvider changed after failed build")

	_, span := tp.Tracer("test").Start(context.Background(), "op")
	defer span.End()
	assert.False(t, span.IsRecording(), "TracerProvider is not shut down after failed build")

	// Повторный Shutdown закрытого MeterProvider возвращает ошибку reader'а.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	assert.ErrorContains(t, mp.Shutdown(ctx), "shutdown", "MeterProvider is not shut down after failed build")
}
