package otelenv_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/maksimegorovdev/delivery-backend/platform/internal/otelenv"
)

// Тест меняет окружение процесса, поэтому без t.Parallel (t.Setenv его и запрещает).
func TestUnset(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")
	t.Setenv("OTEL_EMPTY", "")
	t.Setenv("NOT_OTEL_FOO", "keep")
	t.Setenv("OTELX", "keep")

	otelenv.Unset()

	for _, key := range []string{"OTEL_SDK_DISABLED", "OTEL_TRACES_SAMPLER", "OTEL_EMPTY"} {
		_, ok := os.LookupEnv(key)
		assert.Falsef(t, ok, "%s must be unset", key)
	}
	assert.Equal(t, "keep", os.Getenv("NOT_OTEL_FOO"))
	assert.Equal(t, "keep", os.Getenv("OTELX"))
}
