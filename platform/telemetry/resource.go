package telemetry

import (
	"context"
	"fmt"
	"runtime/debug"

	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const unknownVersion = "unknown"

// newResource собирает Resource сервиса.
//
// Приоритет атрибутов (поздний главнее): сгенерированный service.instance.id,
// OTEL_RESOURCE_ATTRIBUTES, затем service.name и service.version из кода —
// имя сервиса из конфига нельзя переопределить через окружение.
func newResource(ctx context.Context, serviceName string) (*resource.Resource, error) {
	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithService(),
		resource.WithFromEnv(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion(readBuildInfo())),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: resource: %w", err)
	}
	return res, nil
}

func readBuildInfo() *debug.BuildInfo {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return nil
	}
	return info
}

// serviceVersion возвращает vcs.revision из настроек сборки или "unknown".
func serviceVersion(info *debug.BuildInfo) string {
	if info == nil {
		return unknownVersion
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			return s.Value
		}
	}
	return unknownVersion
}
