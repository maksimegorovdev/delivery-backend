//go:build e2e

package e2e_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/e2e"
)

// fakeLookup подменяет [os.LookupEnv]: тесты не трогают окружение процесса и идут параллельно.
func fakeLookup(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]

		return v, ok
	}
}

func TestLoad_Defaults(t *testing.T) {
	t.Parallel()

	got := e2e.Load(fakeLookup(nil))

	require.Equal(t, e2e.Stack{
		GatewayURL:      "http://localhost:28080",
		OrderGRPCAddr:   "localhost:28090",
		UserGRPCAddr:    "localhost:28091",
		ProductGRPCAddr: "localhost:28092",
		UserDSN:         "postgres://postgres:postgres@localhost:25433/userdb?default_transaction_read_only=on",
		ProductDSN:      "postgres://postgres:postgres@localhost:25434/productdb?default_transaction_read_only=on",
		NotificationDSN: "postgres://postgres:postgres@localhost:25435/notificationdb?default_transaction_read_only=on",
		DebeziumURL:     "http://localhost:28083",
		ClickHouseURL:   "http://grafana_reader:grafana_reader@localhost:28123",
	}, got)
}

func TestLoad_Override(t *testing.T) {
	t.Parallel()

	got := e2e.Load(fakeLookup(map[string]string{
		"E2E_GATEWAY_URL":      "http://gateway.test:8080",
		"E2E_USER_GRPC_ADDR":   "user.test:9090",
		"E2E_NOTIFICATION_DSN": "postgres://u:p@db.test:5432/n",
	}))

	require.Equal(t, "http://gateway.test:8080", got.GatewayURL)
	require.Equal(t, "user.test:9090", got.UserGRPCAddr)
	require.Equal(t, "postgres://u:p@db.test:5432/n", got.NotificationDSN)
	// Остальное остаётся по умолчанию.
	require.Equal(t, "localhost:28090", got.OrderGRPCAddr)
	require.Equal(t, "http://localhost:28083", got.DebeziumURL)
}

func TestLoad_EmptyValueFallsBackToDefault(t *testing.T) {
	t.Parallel()

	got := e2e.Load(fakeLookup(map[string]string{"E2E_GATEWAY_URL": ""}))

	require.Equal(t, "http://localhost:28080", got.GatewayURL)
}
