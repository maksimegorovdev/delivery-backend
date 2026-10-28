package telemetry

import (
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/runtime"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// startRuntimeMetrics регистрирует метрики Go runtime (горутины, куча, GC, планировщик)
// в mp. Отдельных горутин не запускает: значения читаются callback'ами при каждом экспорте.
func startRuntimeMetrics(mp *sdkmetric.MeterProvider) error {
	if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
		return fmt.Errorf("telemetry: runtime metrics: %w", err)
	}
	return nil
}
