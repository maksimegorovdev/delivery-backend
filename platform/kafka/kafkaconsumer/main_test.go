//nolint:testpackage // тест использует неэкспортируемые символы пакета
package kafkaconsumer

import (
	"testing"

	"go.uber.org/goleak"

	"github.com/maksimegorovdev/delivery-backend/platform/internal/otelenv"
)

func TestMain(m *testing.M) {
	otelenv.Unset()
	// Общий контейнер testkit живёт до конца бинаря: Ryuk-reaper и keep-alive соединения
	// docker-клиента testcontainers не относятся к проверяемому коду.
	goleak.VerifyTestMain(m,
		goleak.IgnoreAnyFunction("github.com/testcontainers/testcontainers-go.(*Reaper).connect.func1"),
		goleak.IgnoreAnyFunction("net/http.(*persistConn).readLoop"),
		goleak.IgnoreAnyFunction("net/http.(*persistConn).writeLoop"),
	)
}
