// Package e2e описывает адреса стека delivery-e2e для сквозных тестов.
//
// Сами тесты лежат в пакете e2e_test и собираются только с тегом e2e. Файл env.go тега не имеет:
// без него модуль компилируется, и `task test` по корню репозитория его не запускает.
package e2e

// Адреса по умолчанию: порты на хосте, которые публикует e2e/compose.yaml (проект delivery-e2e).
// Диапазоны 25xxx и 28xxx выбраны вне портов dev-стека, чтобы оба стека работали одновременно.
const (
	defaultGatewayURL      = "http://localhost:28080"
	defaultOrderGRPCAddr   = "localhost:28090"
	defaultUserGRPCAddr    = "localhost:28091"
	defaultProductGRPCAddr = "localhost:28092"
	defaultDebeziumURL     = "http://localhost:28083"
)

// Адреса с учётными данными. Они совпадают с dev-стеком (compose.yaml): стек e2e локальный
// и живёт без томов. БД тест только читает: сессия read-only, запись отклоняется самим Postgres.
//
//nolint:gosec // это публичные dev-пароли локального стека из compose.yaml, а не секреты
const (
	defaultClickHouseURL   = "http://grafana_reader:grafana_reader@localhost:28123"
	defaultUserDSN         = "postgres://postgres:postgres@localhost:25433/userdb?default_transaction_read_only=on"
	defaultProductDSN      = "postgres://postgres:postgres@localhost:25434/productdb?default_transaction_read_only=on"
	defaultNotificationDSN = "postgres://postgres:postgres@localhost:25435/notificationdb?default_transaction_read_only=on"
)

// Stack — адреса компонентов стека, к которым ходят тесты.
type Stack struct {
	// GatewayURL — публичный HTTP API; единственная точка входа тестов.
	GatewayURL string
	// OrderGRPCAddr, UserGRPCAddr и ProductGRPCAddr — gRPC-адреса сервисов, только для health.
	OrderGRPCAddr   string
	UserGRPCAddr    string
	ProductGRPCAddr string
	// UserDSN, ProductDSN и NotificationDSN — read-only подключения к БД сервисов.
	UserDSN         string
	ProductDSN      string
	NotificationDSN string
	// DebeziumURL — REST Kafka Connect, статус коннектора.
	DebeziumURL string
	// ClickHouseURL — HTTP-интерфейс ClickHouse с пользователем grafana_reader (только чтение).
	ClickHouseURL string
}

// Load собирает адреса стека: значение берётся из переменной E2E_*, а если она не задана или
// пуста — из значения по умолчанию. lookup обычно [os.LookupEnv]; параметр позволяет тестировать
// разбор без изменения окружения процесса.
//
// Переменные: E2E_GATEWAY_URL, E2E_ORDER_GRPC_ADDR, E2E_USER_GRPC_ADDR, E2E_PRODUCT_GRPC_ADDR,
// E2E_USER_DSN, E2E_PRODUCT_DSN, E2E_NOTIFICATION_DSN, E2E_DEBEZIUM_URL, E2E_CLICKHOUSE_URL.
func Load(lookup func(key string) (string, bool)) Stack {
	get := func(key, fallback string) string {
		if v, ok := lookup(key); ok && v != "" {
			return v
		}

		return fallback
	}

	return Stack{
		GatewayURL:      get("E2E_GATEWAY_URL", defaultGatewayURL),
		OrderGRPCAddr:   get("E2E_ORDER_GRPC_ADDR", defaultOrderGRPCAddr),
		UserGRPCAddr:    get("E2E_USER_GRPC_ADDR", defaultUserGRPCAddr),
		ProductGRPCAddr: get("E2E_PRODUCT_GRPC_ADDR", defaultProductGRPCAddr),
		UserDSN:         get("E2E_USER_DSN", defaultUserDSN),
		ProductDSN:      get("E2E_PRODUCT_DSN", defaultProductDSN),
		NotificationDSN: get("E2E_NOTIFICATION_DSN", defaultNotificationDSN),
		DebeziumURL:     get("E2E_DEBEZIUM_URL", defaultDebeziumURL),
		ClickHouseURL:   get("E2E_CLICKHOUSE_URL", defaultClickHouseURL),
	}
}
