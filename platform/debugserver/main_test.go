package debugserver_test

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// После принудительного закрытия (TestServe_ShutdownTimeoutForcesClose) net/http держит
		// соединение ещё 500 мс (rstAvoidanceDelay) и только потом завершает горутину: это
		// ограниченная задержка stdlib, а не утечка.
		goleak.IgnoreAnyFunction("net/http.(*conn).closeWriteAndWait"),
	)
}
