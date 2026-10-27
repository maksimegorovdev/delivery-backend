package telemetry_test

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"

	"github.com/maksimegorovdev/delivery-backend/platform/telemetry"
)

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
		if err := tel.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
}

func TestNew_InvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  telemetry.Config
	}{
		{name: "пустой Endpoint", cfg: telemetry.Config{Enabled: true, ServiceName: "order"}},
		{name: "пустой ServiceName", cfg: telemetry.Config{Enabled: true, Endpoint: "localhost:4317"}},
		{name: "всё пусто", cfg: telemetry.Config{Enabled: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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
