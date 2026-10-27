package config_test

import (
	"testing"

	"github.com/maksimegorovdev/delivery-backend/services/product/internal/config"
)

// setBaseEnv задаёт минимально валидное окружение без OTEL_* переменных.
func setBaseEnv(t *testing.T) {
	t.Helper()

	t.Setenv("APP_NAME", "product")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("PG_DSN", "postgresql://localhost/productdb")
	t.Setenv("PG_MAX_CONNS", "10")
	t.Setenv("GRPC_SERVER_ADDR", ":8092")
	t.Setenv("GRPC_SERVER_REFLECTION", "true")
}

func TestNew_TelemetryInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "OTEL_ENABLED не задан",
			env:  map[string]string{},
		},
		{
			name: "enabled без endpoint",
			env:  map[string]string{"OTEL_ENABLED": "true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			if _, err := config.New(); err == nil {
				t.Fatal("New() error = nil, want error")
			}
		})
	}
}

func TestNew_TelemetryValid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want config.Telemetry
	}{
		{
			name: "disabled без endpoint",
			env:  map[string]string{"OTEL_ENABLED": "false"},
			want: config.Telemetry{Enabled: false},
		},
		{
			name: "enabled с endpoint",
			env: map[string]string{
				"OTEL_ENABLED":                "true",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "localhost:4317",
			},
			want: config.Telemetry{Enabled: true, Endpoint: "localhost:4317"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := config.New()
			if err != nil {
				t.Fatalf("New() unexpected error: %v", err)
			}
			if cfg.Telemetry != tt.want {
				t.Errorf("Telemetry = %+v, want %+v", cfg.Telemetry, tt.want)
			}
		})
	}
}
