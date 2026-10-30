package config_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/config"
)

// setBaseEnv задаёт минимально валидное окружение без OTEL_* переменных.
// OTEL_* удаляются явно: иначе экспортированные в shell значения подменят кейсы.
func setBaseEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{"OTEL_ENABLED", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		t.Setenv(key, "") // восстанавливает исходное значение после теста
		require.NoError(t, os.Unsetenv(key))
	}

	t.Setenv("APP_NAME", "user")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("PG_DSN", "postgresql://localhost/userdb")
	t.Setenv("PG_MAX_CONNS", "10")
	t.Setenv("GRPC_SERVER_ADDR", ":8091")
	t.Setenv("GRPC_SERVER_REFLECTION", "true")
}

func TestNew_TelemetryInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{
			name: "OTEL_ENABLED unset",
			env:  map[string]string{},
		},
		{
			name: "enabled without endpoint",
			env:  map[string]string{"OTEL_ENABLED": "true"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			_, err := config.New()
			require.Error(t, err)
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
			name: "disabled without endpoint",
			env:  map[string]string{"OTEL_ENABLED": "false"},
			want: config.Telemetry{Enabled: false},
		},
		{
			name: "enabled with endpoint",
			env: map[string]string{
				"OTEL_ENABLED":                "true",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://localhost:4317",
			},
			want: config.Telemetry{Enabled: true, Endpoint: "http://localhost:4317"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			cfg, err := config.New()
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.Telemetry)
		})
	}
}

func TestNew_GRPCServerReflection(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "enabled", value: "true", want: true},
		{name: "disabled", value: "false", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv("OTEL_ENABLED", "false")
			t.Setenv("GRPC_SERVER_REFLECTION", tt.value)

			cfg, err := config.New()
			require.NoError(t, err)
			assert.Equal(t, tt.want, cfg.GRPCServer.Reflection)
		})
	}
}

func TestNew_GRPCServerReflectionUnset(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("OTEL_ENABLED", "false")
	t.Setenv("GRPC_SERVER_REFLECTION", "") // восстанавливает исходное значение после теста
	require.NoError(t, os.Unsetenv("GRPC_SERVER_REFLECTION"))

	_, err := config.New()
	require.Error(t, err)
}

func TestNew_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "APP_NAME empty", key: "APP_NAME", value: ""},
		{name: "LOG_LEVEL unknown", key: "LOG_LEVEL", value: "trace"},
		{name: "LOG_FORMAT unknown", key: "LOG_FORMAT", value: "xml"},
		{name: "PG_DSN empty", key: "PG_DSN", value: ""},
		{name: "PG_MAX_CONNS zero", key: "PG_MAX_CONNS", value: "0"},
		{name: "PG_MAX_CONNS negative", key: "PG_MAX_CONNS", value: "-1"},
		{name: "GRPC_SERVER_ADDR empty", key: "GRPC_SERVER_ADDR", value: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv("OTEL_ENABLED", "false")
			t.Setenv(tt.key, tt.value)

			_, err := config.New()
			require.Error(t, err)
		})
	}
}
