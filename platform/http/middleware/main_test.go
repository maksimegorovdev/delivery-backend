package middleware_test

import (
	"testing"

	"github.com/maksimegorovdev/delivery-backend/platform/internal/otelenv"
)

func TestMain(m *testing.M) {
	otelenv.Unset()
	m.Run()
}
