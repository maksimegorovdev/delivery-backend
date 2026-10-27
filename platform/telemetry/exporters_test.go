package telemetry

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestDeltaTemporality(t *testing.T) {
	tests := []struct {
		kind sdkmetric.InstrumentKind
		want metricdata.Temporality
	}{
		{sdkmetric.InstrumentKindCounter, metricdata.DeltaTemporality},
		{sdkmetric.InstrumentKindHistogram, metricdata.DeltaTemporality},
		{sdkmetric.InstrumentKindObservableCounter, metricdata.DeltaTemporality},
		{sdkmetric.InstrumentKindUpDownCounter, metricdata.CumulativeTemporality},
		{sdkmetric.InstrumentKindObservableUpDownCounter, metricdata.CumulativeTemporality},
		{sdkmetric.InstrumentKindObservableGauge, metricdata.CumulativeTemporality},
		{sdkmetric.InstrumentKindGauge, metricdata.CumulativeTemporality},
	}

	for _, tt := range tests {
		t.Run(tt.kind.String(), func(t *testing.T) {
			if got := deltaTemporality(tt.kind); got != tt.want {
				t.Errorf("deltaTemporality(%v) = %v, want %v", tt.kind, got, tt.want)
			}
		})
	}
}

// countingExporter считает вызовы Export; остальные методы — заглушки.
type countingExporter struct {
	sdkmetric.Exporter // nil: Temporality/Aggregation в тестах не вызываются
	exports            atomic.Int32
}

func (e *countingExporter) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (e *countingExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}

func (e *countingExporter) Export(context.Context, *metricdata.ResourceMetrics) error {
	e.exports.Add(1)
	return nil
}

func (e *countingExporter) ForceFlush(context.Context) error { return nil }
func (e *countingExporter) Shutdown(context.Context) error   { return nil }

func TestNewPeriodicReader_Interval(t *testing.T) {
	tests := []struct {
		name     string
		env      string // "" — OTEL_METRIC_EXPORT_INTERVAL не задан
		before   time.Duration
		wantPre  int32 // экспортов к моменту before
		wantPost int32 // экспортов через ещё 2s после before
	}{
		{name: "по умолчанию 10s", env: "", before: 9 * time.Second, wantPre: 0, wantPost: 1},
		{name: "OTEL_METRIC_EXPORT_INTERVAL=3000 переопределяет", env: "3000", before: 2 * time.Second, wantPre: 0, wantPost: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env == "" {
				t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "")
				if err := os.Unsetenv("OTEL_METRIC_EXPORT_INTERVAL"); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", tt.env)
			}

			synctest.Test(t, func(t *testing.T) {
				exp := &countingExporter{}
				reader := newPeriodicReader(exp)
				mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
				defer func() { _ = mp.Shutdown(context.Background()) }()

				time.Sleep(tt.before)
				synctest.Wait()
				if got := exp.exports.Load(); got != tt.wantPre {
					t.Errorf("экспортов за %v = %d, want %d", tt.before, got, tt.wantPre)
				}

				time.Sleep(2 * time.Second)
				synctest.Wait()
				if got := exp.exports.Load(); got != tt.wantPost {
					t.Errorf("экспортов после интервала = %d, want %d", got, tt.wantPost)
				}
			})
		})
	}
}
