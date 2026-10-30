//nolint:testpackage // тест использует неэкспортируемые символы пакета
package telemetry

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestDeltaTemporality(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

			assert.Equal(t, tt.want, deltaTemporality(tt.kind))
		})
	}
}

// countingExporter считает вызовы Export; остальные методы — заглушки.
type countingExporter struct {
	sdkmetric.Exporter // nil: Temporality/Aggregation в тестах не вызываются

	exports atomic.Int32
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

func TestSpanLimits_TruncatesLongAttribute(t *testing.T) {
	tests := []struct {
		name string
		env  string // "" — OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT не задан
		want int
	}{
		{name: "default 1024", env: "", want: defaultAttributeValueLengthLimit},
		{name: "env overrides", env: "16", want: 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT", "")
			t.Setenv("OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT", tt.env)
			if tt.env == "" {
				require.NoError(t, os.Unsetenv("OTEL_SPAN_ATTRIBUTE_VALUE_LENGTH_LIMIT"))
			}
			require.NoError(t, os.Unsetenv("OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT"))

			exp := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp), sdktrace.WithRawSpanLimits(spanLimits()))
			defer func() { _ = tp.Shutdown(context.Background()) }()

			_, span := tp.Tracer("test").Start(context.Background(), "op")
			span.SetAttributes(attribute.String("user_agent.original", strings.Repeat("a", 4096)))
			span.End()

			got := exp.GetSpans()
			require.Len(t, got, 1)
			assert.Len(t, got[0].Attributes[0].Value.AsString(), tt.want)
		})
	}
}

func TestLoggerProviderOptions_AttributeLimit(t *testing.T) {
	tests := []struct {
		name string
		env  string // "" — OTEL_LOGRECORD_ATTRIBUTE_VALUE_LENGTH_LIMIT не задан
		want int
	}{
		{name: "default 1024", env: "", want: defaultAttributeValueLengthLimit},
		{name: "env overrides", env: "16", want: 16},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(envLogAttributeValueLengthLimit, tt.env)
			if tt.env == "" {
				require.NoError(t, os.Unsetenv(envLogAttributeValueLengthLimit))
			}

			exp := &recordingLogExporter{}
			lp := sdklog.NewLoggerProvider(loggerProviderOptions(exp, resource.Empty())...)

			var rec log.Record
			rec.AddAttributes(attribute.String("http.path", strings.Repeat("a", 4096)))
			lp.Logger("test").Emit(context.Background(), rec)
			require.NoError(t, lp.Shutdown(context.Background()))

			require.Len(t, exp.records, 1)
			var n int
			exp.records[0].WalkAttributes(func(kv attribute.KeyValue) bool {
				n = len(kv.Value.AsString())
				return false
			})
			assert.Equal(t, tt.want, n)
		})
	}
}

// recordingLogExporter запоминает экспортированные записи логов.
type recordingLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (e *recordingLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, r := range records {
		e.records = append(e.records, r.Clone())
	}
	return nil
}

func (e *recordingLogExporter) Shutdown(context.Context) error   { return nil }
func (e *recordingLogExporter) ForceFlush(context.Context) error { return nil }

func TestNewPeriodicReader_Interval(t *testing.T) {
	tests := []struct {
		name     string
		env      string // "" — OTEL_METRIC_EXPORT_INTERVAL не задан
		before   time.Duration
		wantPre  int32 // экспортов к моменту before
		wantPost int32 // экспортов через ещё 2s после before
	}{
		{name: "default 10s", env: "", before: 9 * time.Second, wantPre: 0, wantPost: 1},
		{
			name:     "OTEL_METRIC_EXPORT_INTERVAL=3000 overrides",
			env:      "3000",
			before:   2 * time.Second,
			wantPre:  0,
			wantPost: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env == "" {
				t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "")
				require.NoError(t, os.Unsetenv("OTEL_METRIC_EXPORT_INTERVAL"))
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
				assert.Equal(t, tt.wantPre, exp.exports.Load(), "exports before the interval")

				time.Sleep(2 * time.Second)
				synctest.Wait()
				assert.Equal(t, tt.wantPost, exp.exports.Load(), "exports after the interval")
			})
		})
	}
}
