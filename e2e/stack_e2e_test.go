//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/maksimegorovdev/delivery-backend/e2e"
)

const (
	// startupTimeout — общий предел ожидания готовности стека в TestMain.
	startupTimeout = 2 * time.Minute
	// pollInterval — период повторных попыток при ожидании готовности.
	pollInterval = time.Second
	// attemptTimeout — предел одной попытки проверки готовности.
	attemptTimeout = 3 * time.Second

	connectorName = "order-outbox"
	stateRunning  = "RUNNING"

	seedUserEmail   = "demo@example.com"
	seedBorjomiName = "Минеральная вода Borjomi"
	seedColaName    = "Coca-Cola"
)

// seedProduct — товар из seed-данных сервиса product.
type seedProduct struct {
	ID    string
	Name  string
	Price int64
}

// seedData — id и значения seed-данных, найденные SELECT-ом: публичного API для их списка нет.
type seedData struct {
	UserID    string
	AddressID string
	Address   string
	Borjomi   seedProduct
	Cola      seedProduct
}

// Состояние, которое TestMain готовит один раз, а тесты только читают.
//
//nolint:gochecknoglobals // общий для всех тестов адрес стека, seed и подключение к БД notification
var (
	stack          e2e.Stack
	seed           seedData
	notificationDB *pgxpool.Pool
	httpClient     = &http.Client{Timeout: requestTimeout}
)

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

// run проверяет готовность стека и запускает тесты. Если шаг готовности не прошёл, он печатает
// его название и возвращает 1, тесты не запускаются.
func run(m *testing.M) int {
	stack = e2e.Load(os.LookupEnv)

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()

	if err := prepare(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: стек не готов: %v\n", err)

		return 1
	}
	defer notificationDB.Close()

	return m.Run()
}

// readinessStep — один шаг проверки готовности стека.
type readinessStep struct {
	name string
	run  func(ctx context.Context) error
}

// prepare проходит шаги готовности по порядку; ошибка называет шаг, на котором стек не дождались.
func prepare(ctx context.Context) error {
	steps := []readinessStep{
		{"debezium: коннектор " + connectorName + " в состоянии RUNNING", checkConnector},
		{"health: order " + stack.OrderGRPCAddr, healthStep(stack.OrderGRPCAddr)},
		{"health: user " + stack.UserGRPCAddr, healthStep(stack.UserGRPCAddr)},
		{"health: product " + stack.ProductGRPCAddr, healthStep(stack.ProductGRPCAddr)},
		{"gateway: 400 на POST /api/v1/orders с пустым телом", checkGateway},
		{"seed-id: адрес и товары через SELECT", loadSeed},
		{"подключение к БД notification", connectNotificationDB},
	}

	for _, step := range steps {
		if err := waitFor(ctx, step.run); err != nil {
			return fmt.Errorf("шаг %q: %w", step.name, err)
		}
	}

	return nil
}

// waitFor повторяет check, пока он не вернёт nil или не истечёт ctx. Каждая попытка ограничена
// attemptTimeout. При таймауте в ошибку попадает последняя ошибка проверки.
func waitFor(ctx context.Context, check func(ctx context.Context) error) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	var last error

	for {
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		last = check(attemptCtx)
		cancel()

		if last == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: последняя ошибка: %w", ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

// connectorStatus — ответ GET /connectors/{name}/status Kafka Connect.
type connectorStatus struct {
	Connector struct {
		State string `json:"state"`
	} `json:"connector"`
	Tasks []struct {
		State string `json:"state"`
	} `json:"tasks"`
}

// checkConnector требует, чтобы коннектор и все его задачи были RUNNING.
func checkConnector(ctx context.Context) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		stack.DebeziumURL+"/connectors/"+connectorName+"/status",
		nil,
	)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("get connector status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("get connector status: unexpected status %d", resp.StatusCode)
	}

	var status connectorStatus
	if decodeErr := json.NewDecoder(resp.Body).Decode(&status); decodeErr != nil {
		return fmt.Errorf("decode connector status: %w", decodeErr)
	}

	if status.Connector.State != stateRunning {
		return fmt.Errorf("connector state %q", status.Connector.State)
	}

	if len(status.Tasks) == 0 {
		return errors.New("connector has no tasks")
	}

	for i, task := range status.Tasks {
		if task.State != stateRunning {
			return fmt.Errorf("task %d state %q", i, task.State)
		}
	}

	return nil
}

// healthStep возвращает проверку gRPC health для сервиса по адресу.
func healthStep(addr string) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return checkHealth(ctx, addr)
	}
}

// checkHealth требует статус SERVING от стандартного gRPC health сервиса по адресу.
func checkHealth(ctx context.Context, addr string) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	defer func() { _ = conn.Close() }()

	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		return fmt.Errorf("health check %s: %w", addr, err)
	}

	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("health %s: status %s", addr, resp.GetStatus())
	}

	return nil
}

// checkGateway требует 400 от gateway на пустое тело: HTTP-сервер принимает запросы, а валидация работает.
func checkGateway(ctx context.Context) error {
	status, _, err := postOrder(ctx, nil)
	if err != nil {
		return err
	}

	if status != http.StatusBadRequest {
		return fmt.Errorf("unexpected status %d, want %d", status, http.StatusBadRequest)
	}

	return nil
}

// loadSeed находит id seed-данных: адрес пользователя по email и товары по имени.
func loadSeed(ctx context.Context) error {
	userDB, err := connectDB(ctx, stack.UserDSN)
	if err != nil {
		return fmt.Errorf("user db: %w", err)
	}
	defer userDB.Close()

	productDB, err := connectDB(ctx, stack.ProductDSN)
	if err != nil {
		return fmt.Errorf("product db: %w", err)
	}
	defer productDB.Close()

	var found seedData

	err = userDB.QueryRow(ctx, `
		SELECT u.id::text, a.id::text, a.address
		FROM users u
		JOIN addresses a ON a.user_id = u.id
		WHERE u.email = $1
		ORDER BY a.created_at
		LIMIT 1`, seedUserEmail).Scan(&found.UserID, &found.AddressID, &found.Address)
	if err != nil {
		return fmt.Errorf("select seed address of %s: %w", seedUserEmail, err)
	}

	if found.Borjomi, err = selectProduct(ctx, productDB, seedBorjomiName); err != nil {
		return err
	}

	if found.Cola, err = selectProduct(ctx, productDB, seedColaName); err != nil {
		return err
	}

	seed = found

	return nil
}

// selectProduct читает seed-товар по имени.
func selectProduct(ctx context.Context, pool *pgxpool.Pool, name string) (seedProduct, error) {
	product := seedProduct{Name: name}

	err := pool.QueryRow(ctx, `SELECT id::text, price FROM products WHERE name = $1`, name).
		Scan(&product.ID, &product.Price)
	if err != nil {
		return seedProduct{}, fmt.Errorf("select product %q: %w", name, err)
	}

	return product, nil
}

// connectNotificationDB открывает пул к БД notification, из которой тесты читают результат.
func connectNotificationDB(ctx context.Context) error {
	pool, err := connectDB(ctx, stack.NotificationDSN)
	if err != nil {
		return fmt.Errorf("notification db: %w", err)
	}

	if notificationDB != nil {
		notificationDB.Close()
	}

	notificationDB = pool

	return nil
}

// connectDB открывает пул и проверяет соединение. DSN из env.go содержит
// default_transaction_read_only=on: запись отклоняется самим Postgres.
func connectDB(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}

	if pingErr := pool.Ping(ctx); pingErr != nil {
		pool.Close()

		return nil, fmt.Errorf("ping: %w", pingErr)
	}

	return pool, nil
}
