package telemetry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"

	"github.com/maksimegorovdev/delivery-backend/platform/internal/otelenv"
	"github.com/maksimegorovdev/delivery-backend/platform/telemetry"
)

func TestMain(m *testing.M) {
	otelenv.Unset()
	m.Run()
}

//nolint:paralleltest // читает глобальные провайдеры OTel, тест нельзя запускать параллельно
func TestNew_Disabled(t *testing.T) {
	tracerBefore := otel.GetTracerProvider()
	meterBefore := otel.GetMeterProvider()
	loggerBefore := otel.GetLoggerProvider()

	tel, err := telemetry.New(context.Background(), telemetry.Config{Enabled: false})
	require.NoError(t, err)

	t.Run("does not change global providers", func(t *testing.T) {
		assert.Same(t, tracerBefore, otel.GetTracerProvider())
		assert.Same(t, meterBefore, otel.GetMeterProvider())
		assert.Same(t, loggerBefore, otel.GetLoggerProvider())
	})

	t.Run("LoggerProvider is not nil", func(t *testing.T) {
		assert.NotNil(t, tel.LoggerProvider())
	})

	t.Run("Shutdown returns nil", func(t *testing.T) {
		assert.NoError(t, tel.Shutdown(context.Background()))
	})
}

func TestNew_InvalidConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  telemetry.Config
	}{
		{name: "empty Endpoint", cfg: telemetry.Config{Enabled: true, ServiceName: "order"}},
		{name: "empty ServiceName", cfg: telemetry.Config{Enabled: true, Endpoint: "http://localhost:4317"}},
		{
			name: "Endpoint without scheme",
			cfg:  telemetry.Config{Enabled: true, ServiceName: "order", Endpoint: "localhost:4317"},
		},
		{
			name: "Endpoint IP without scheme",
			cfg:  telemetry.Config{Enabled: true, ServiceName: "order", Endpoint: "127.0.0.1:4317"},
		},
		{
			name: "Endpoint with grpc scheme",
			cfg:  telemetry.Config{Enabled: true, ServiceName: "order", Endpoint: "grpc://localhost:4317"},
		},
		{
			name: "Endpoint without host",
			cfg:  telemetry.Config{Enabled: true, ServiceName: "order", Endpoint: "http://"},
		},
		{name: "everything empty", cfg: telemetry.Config{Enabled: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tel, err := telemetry.New(context.Background(), tt.cfg)
			require.Error(t, err)
			assert.Nil(t, tel)
			assert.True(t, strings.HasPrefix(err.Error(), "telemetry: "), "error without prefix: %v", err)
		})
	}
}
