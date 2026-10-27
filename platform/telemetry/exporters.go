package telemetry

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// defaultMetricInterval — период экспорта метрик, если не задан OTEL_METRIC_EXPORT_INTERVAL.
const defaultMetricInterval = 10 * time.Second

// newTracerProvider создаёт TracerProvider с OTLP gRPC экспортёром.
//
// Сэмплер не задаётся явно: SDK читает OTEL_TRACES_SAMPLER/OTEL_TRACES_SAMPLER_ARG,
// а без них использует ParentBased(AlwaysSample).
func newTracerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdktrace.TracerProvider, error) {
	exp, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpointURL(cfg.Endpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: trace exporter: %w", err)
	}

	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	), nil
}

// newMeterProvider создаёт MeterProvider с OTLP gRPC экспортёром
// и delta temporality для счётчиков и гистограмм.
func newMeterProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
	exp, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithEndpointURL(cfg.Endpoint),
		otlpmetricgrpc.WithTemporalitySelector(deltaTemporality),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: metric exporter: %w", err)
	}

	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(newPeriodicReader(exp)),
		sdkmetric.WithResource(res),
	), nil
}

// newLoggerProvider создаёт LoggerProvider с OTLP gRPC экспортёром и batch-процессором.
func newLoggerProvider(ctx context.Context, cfg Config, res *resource.Resource) (*sdklog.LoggerProvider, error) {
	exp, err := otlploggrpc.New(ctx,
		otlploggrpc.WithEndpointURL(cfg.Endpoint),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: log exporter: %w", err)
	}

	return sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		sdklog.WithResource(res),
	), nil
}

// newPeriodicReader создаёт reader с интервалом defaultMetricInterval. Если задан
// OTEL_METRIC_EXPORT_INTERVAL, интервал не передаётся явно: WithInterval перекрыл бы env.
func newPeriodicReader(exp sdkmetric.Exporter) *sdkmetric.PeriodicReader {
	var opts []sdkmetric.PeriodicReaderOption
	if os.Getenv("OTEL_METRIC_EXPORT_INTERVAL") == "" {
		opts = append(opts, sdkmetric.WithInterval(defaultMetricInterval))
	}
	return sdkmetric.NewPeriodicReader(exp, opts...)
}

// deltaTemporality выбирает Delta для аддитивных монотонных инструментов
// (Counter, Histogram и Observable Counter), Cumulative — для остальных.
func deltaTemporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	//nolint:exhaustive // все остальные виды инструментов получают Cumulative в default
	switch kind {
	case sdkmetric.InstrumentKindCounter,
		sdkmetric.InstrumentKindHistogram,
		sdkmetric.InstrumentKindObservableCounter:
		return metricdata.DeltaTemporality
	default:
		return metricdata.CumulativeTemporality
	}
}
