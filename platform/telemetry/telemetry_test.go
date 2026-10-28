package telemetry_test

import (
	"context"
	"strings"
	"testing"

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
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	t.Run("не меняет глобальные провайдеры", func(t *testing.T) {
		if got := otel.GetTracerProvider(); got != tracerBefore {
			t.Errorf("TracerProvider изменился: %T -> %T", tracerBefore, got)
		}
		if got := otel.GetMeterProvider(); got != meterBefore {
			t.Errorf("MeterProvider изменился: %T -> %T", meterBefore, got)
		}
		if got := otel.GetLoggerProvider(); got != loggerBefore {
			t.Errorf("LoggerProvider изменился: %T -> %T", loggerBefore, got)
		}
	})

	t.Run("LoggerProvider не nil", func(t *testing.T) {
		if tel.LoggerProvider() == nil {
			t.Error("LoggerProvider() == nil")
		}
	})

	t.Run("Shutdown возвращает nil", func(t *testing.T) {
		if shutdownErr := tel.Shutdown(context.Background()); shutdownErr != nil {
			t.Errorf("Shutdown: %v", shutdownErr)
		}
	})
}

func TestNew_InvalidConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  telemetry.Config
	}{
		{name: "пустой Endpoint", cfg: telemetry.Config{Enabled: true, ServiceName: "order"}},
		{name: "пустой ServiceName", cfg: telemetry.Config{Enabled: true, Endpoint: "http://localhost:4317"}},
		{
			name: "Endpoint без схемы",
			cfg:  telemetry.Config{Enabled: true, ServiceName: "order", Endpoint: "localhost:4317"},
		},
		{
			name: "Endpoint IP без схемы",
			cfg:  telemetry.Config{Enabled: true, ServiceName: "order", Endpoint: "127.0.0.1:4317"},
		},
		{
			name: "Endpoint со схемой grpc",
			cfg:  telemetry.Config{Enabled: true, ServiceName: "order", Endpoint: "grpc://localhost:4317"},
		},
		{
			name: "Endpoint без хоста",
			cfg:  telemetry.Config{Enabled: true, ServiceName: "order", Endpoint: "http://"},
		},
		{name: "всё пусто", cfg: telemetry.Config{Enabled: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tel, err := telemetry.New(context.Background(), tt.cfg)
			if err == nil {
				t.Fatalf("New: ожидалась ошибка, получен %v", tel)
			}
			if !strings.HasPrefix(err.Error(), "telemetry: ") {
				t.Errorf("ошибка без префикса %q: %v", "telemetry: ", err)
			}
		})
	}
}
