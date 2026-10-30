package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

//nolint:paralleltest // регистрирует глобальные провайдеры OTel, тест нельзя запускать параллельно
func TestNew_Enabled_RegistersLoggerProvider(t *testing.T) {
	tel := newEnabled(t)

	lp, ok := tel.LoggerProvider().(*sdklog.LoggerProvider)
	require.True(t, ok, "LoggerProvider() = %T, want *sdklog.LoggerProvider", tel.LoggerProvider())
	assert.Same(t, log.LoggerProvider(lp), otel.GetLoggerProvider(),
		"global LoggerProvider differs from Telemetry.LoggerProvider()")
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
	require.NoError(t, err)
	counter.Add(ctx, 1)

	var rec log.Record
	rec.SetBody(attribute.StringValue("hello"))
	tel.LoggerProvider().Logger("test").Emit(ctx, rec)

	shutdownCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	start := time.Now()
	_ = tel.Shutdown(shutdownCtx)
	assert.LessOrEqual(t, time.Since(start), deadline+slack, "Shutdown must return by the context deadline")
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
