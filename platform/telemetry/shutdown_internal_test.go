package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// blockingSpanExporter висит в Export до отмены ctx, как экспорт в недоступный Collector.
type blockingSpanExporter struct{}

func (blockingSpanExporter) ExportSpans(ctx context.Context, _ []sdktrace.ReadOnlySpan) error {
	<-ctx.Done()
	return ctx.Err()
}

func (blockingSpanExporter) Shutdown(context.Context) error { return nil }

// Зависший экспорт трейсов не должен лишать метрики финального экспорта.
func TestShutdown_HungTraceExportDoesNotStarveMetrics(t *testing.T) {
	t.Parallel()

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(blockingSpanExporter{}))
	_, span := tp.Tracer("test").Start(context.Background(), "op")
	span.End()

	metricExp := &countingExporter{}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp)))
	counter, err := mp.Meter("test").Int64Counter("requests")
	require.NoError(t, err)
	counter.Add(context.Background(), 1)

	tel := &Telemetry{tracerProvider: tp, meterProvider: mp}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	require.Error(t, tel.Shutdown(ctx), "hung trace export must surface as a Shutdown error")

	assert.Equal(t, int32(1), metricExp.exports.Load(), "metric exports during Shutdown")
}
