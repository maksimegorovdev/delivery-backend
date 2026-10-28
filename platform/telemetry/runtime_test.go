//nolint:testpackage // тест использует неэкспортируемые символы пакета
package telemetry

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

// collectMetricNames возвращает имена метрик, собранные reader'ом.
func collectMetricNames(t *testing.T, reader sdkmetric.Reader) map[string]bool {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	names := make(map[string]bool)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			names[m.Name] = true
		}
	}
	return names
}

// manualMeterFactory подставляет MeterProvider с ManualReader вместо OTLP-экспортёра.
type meterFactory = func(context.Context, Config, *resource.Resource) (*sdkmetric.MeterProvider, error)

func manualMeterFactory(reader sdkmetric.Reader) meterFactory {
	return func(_ context.Context, _ Config, res *resource.Resource) (*sdkmetric.MeterProvider, error) {
		return sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(reader),
			sdkmetric.WithResource(res),
		), nil
	}
}

// При Enabled=true build регистрирует runtime-метрики Go: горутины, куча, GC.
//
//nolint:paralleltest // регистрирует глобальные провайдеры OTel, тест нельзя запускать параллельно
func TestBuild_Enabled_RegistersRuntimeMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	f := defaultFactories()
	f.meter = manualMeterFactory(reader)

	cfg := Config{Enabled: true, ServiceName: "test-service", Endpoint: "http://127.0.0.1:1"}
	tel, err := build(context.Background(), cfg, f)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	t.Cleanup(func() { _ = tel.Shutdown(context.Background()) })

	names := collectMetricNames(t, reader)
	for _, want := range []string{"go.goroutine.count", "go.memory.used", "go.memory.gc.goal"} {
		if !names[want] {
			t.Errorf("нет runtime-метрики %q, собраны: %v", want, keys(names))
		}
	}
}

// При Enabled=false build не создаёт MeterProvider, runtime-метрики не стартуют.
//
//nolint:paralleltest // build читает глобальное состояние OTel
func TestBuild_Disabled_DoesNotStartRuntimeMetrics(t *testing.T) {
	called := false
	f := defaultFactories()
	f.meter = func(context.Context, Config, *resource.Resource) (*sdkmetric.MeterProvider, error) {
		called = true
		return nil, nil //nolint:nilnil // фабрика не должна вызываться
	}

	tel, err := build(context.Background(), Config{Enabled: false}, f)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if called {
		t.Error("MeterProvider создан при Enabled=false")
	}
	if tel.meterProvider != nil {
		t.Error("meterProvider не nil при Enabled=false")
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
