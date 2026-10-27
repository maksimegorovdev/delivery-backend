package telemetry_test

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

//nolint:paralleltest // регистрирует глобальные провайдеры OTel, тест нельзя запускать параллельно
func TestNew_Enabled_RegistersLoggerProvider(t *testing.T) {
	tel := newEnabled(t)

	lp, ok := tel.LoggerProvider().(*sdklog.LoggerProvider)
	if !ok {
		t.Fatalf("LoggerProvider() = %T, want *sdklog.LoggerProvider", tel.LoggerProvider())
	}
	if global := otel.GetLoggerProvider(); global != log.LoggerProvider(lp) {
		t.Errorf("глобальный LoggerProvider = %T, не совпадает с Telemetry.LoggerProvider()", global)
	}
}

// Shutdown с накопленными данными всех трёх сигналов и недоступным Collector
// должен вернуться не позже deadline контекста (риск R4).
//
//nolint:paralleltest // регистрирует глобальные провайдеры OTel, тест нельзя запускать параллельно
func TestShutdown_UnreachableCollectorRespectsDeadline(t *testing.T) {
	const deadline = 2 * time.Second
	const slack = 500 * time.Millisecond

	tel := newEnabled(t)

	ctx := context.Background()
	_, span := otel.Tracer("test").Start(ctx, "op")
	span.End()

	counter, err := otel.Meter("test").Int64Counter("requests")
	if err != nil {
		t.Fatalf("Int64Counter: %v", err)
	}
	counter.Add(ctx, 1)

	var rec log.Record
	rec.SetBody(attribute.StringValue("hello"))
	tel.LoggerProvider().Logger("test").Emit(ctx, rec)

	shutdownCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	start := time.Now()
	_ = tel.Shutdown(shutdownCtx)
	if elapsed := time.Since(start); elapsed > deadline+slack {
		t.Errorf("Shutdown занял %v, deadline %v", elapsed, deadline)
	}
}

//nolint:paralleltest // регистрирует глобальные провайдеры OTel, тест нельзя запускать параллельно
func TestShutdown_Idempotent(t *testing.T) {
	tel := newEnabled(t)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_ = tel.Shutdown(ctx)

	// Повторный вызов (например, closer + ручной Shutdown) не должен паниковать.
	_ = tel.Shutdown(ctx)
}
