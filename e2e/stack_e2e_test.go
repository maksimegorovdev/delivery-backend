//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	// composeProject и composeFile задают стек, которым тесты устойчивости управляют через docker compose.
	// go test запускается в каталоге e2e, поэтому файл указан относительно него.
	composeProject = "delivery-e2e"
	composeFile    = "compose.yaml"
	// composeTimeout — предел одной команды docker compose (stop ждёт graceful shutdown).
	composeTimeout = time.Minute

	serviceNotification = "notification"
	serviceDebezium     = "debezium"
	serviceRedpanda     = "redpanda"

	// consumerGroup — группа, в которой notification читает топик событий заказов.
	consumerGroup = "notification"
	// groupStateEmpty — состояние группы без участников: только тогда rpk разрешает сдвиг оффсетов.
	groupStateEmpty = "Empty"

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

// stopService останавливает сервис стека и регистрирует в t.Cleanup его запуск и ожидание
// готовности, чтобы сервис остался работающим при любом исходе теста.
func stopService(t *testing.T, service string) {
	t.Helper()

	t.Cleanup(func() {
		// t.Context() к этому моменту уже отменён, поэтому контекст свой.
		ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
		defer cancel()

		if err := ensureRunning(ctx, service); err != nil {
			t.Errorf("вернуть %s в рабочее состояние: %v", service, err)
		}
	})

	ctx, cancel := context.WithTimeout(t.Context(), composeTimeout)
	defer cancel()

	require.NoError(t, composeRun(ctx, "stop", service))
}

// startService запускает остановленный сервис и ждёт его готовности.
func startService(t *testing.T, service string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), startupTimeout)
	defer cancel()

	require.NoError(t, ensureRunning(ctx, service))
}

// ensureRunning запускает сервис (для уже работающего это no-op) и ждёт готовности: Debezium —
// коннектор в состоянии RUNNING. У notification порта нет, его готовность доказывает
// обработка события в самом тесте.
func ensureRunning(ctx context.Context, service string) error {
	if err := composeRun(ctx, "start", service); err != nil {
		return err
	}

	if service == serviceDebezium {
		if err := waitFor(ctx, checkConnector); err != nil {
			return fmt.Errorf("wait connector %s: %w", connectorName, err)
		}
	}

	return nil
}

// composeRun выполняет `docker compose` для стека delivery-e2e и включает вывод в ошибку.
func composeRun(ctx context.Context, args ...string) error {
	_, err := composeOutput(ctx, args...)

	return err
}

// composeOutput выполняет `docker compose` для стека delivery-e2e и возвращает stdout.
// При ошибке в неё попадает и stderr команды.
func composeOutput(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"compose", "-f", composeFile, "-p", composeProject}, args...)

	cmd := exec.CommandContext(ctx, "docker", full...)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker %v: %w: %s", args, err, stderr.String())
	}

	return out, nil
}

// groupDescription — нужные поля `rpk group describe --format json`.
type groupDescription struct {
	State    string `json:"state"`
	Members  int    `json:"members"`
	TotalLag int64  `json:"total_lag"`
}

// rpkGroup выполняет `rpk group <args>` в контейнере redpanda и возвращает stdout.
func rpkGroup(ctx context.Context, args ...string) ([]byte, error) {
	full := append([]string{"exec", "-T", serviceRedpanda, "rpk", "group"}, args...)

	return composeOutput(ctx, full...)
}

// describeGroup читает состояние группы notification.
func describeGroup(ctx context.Context) (groupDescription, error) {
	out, err := rpkGroup(ctx, "describe", consumerGroup, "--format", "json")
	if err != nil {
		return groupDescription{}, err
	}

	var groups []groupDescription
	if decodeErr := json.Unmarshal(out, &groups); decodeErr != nil {
		return groupDescription{}, fmt.Errorf("decode group description: %w", decodeErr)
	}

	if len(groups) != 1 {
		return groupDescription{}, fmt.Errorf("group description: got %d groups, want 1", len(groups))
	}

	return groups[0], nil
}

// describeGroupLag возвращает суммарное отставание группы notification.
func describeGroupLag(t *testing.T) int64 {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), composeTimeout)
	defer cancel()

	group, err := describeGroup(ctx)
	require.NoError(t, err)

	return group.TotalLag
}

// awaitGroupEmpty ждёт, пока в группе notification не останется участников.
func awaitGroupEmpty(t *testing.T) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		ctx, cancel := context.WithTimeout(t.Context(), composeTimeout)
		defer cancel()

		group, err := describeGroup(ctx)
		if !assert.NoError(c, err) {
			return
		}

		assert.Equal(c, groupStateEmpty, group.State, "состояние группы %s", consumerGroup)
	}, eventuallyTimeout, eventuallyTick)
}

// awaitGroupCaughtUp ждёт, пока группа notification снова получит участника и дочитает топик до конца.
func awaitGroupCaughtUp(t *testing.T) {
	t.Helper()

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		ctx, cancel := context.WithTimeout(t.Context(), composeTimeout)
		defer cancel()

		group, err := describeGroup(ctx)
		if !assert.NoError(c, err) {
			return
		}

		assert.Positive(c, group.Members, "участники группы %s", consumerGroup)
		assert.Zero(c, group.TotalLag, "отставание группы %s", consumerGroup)
	}, eventuallyTimeout, eventuallyTick)
}

// seekGroupToStart сдвигает оффсеты группы notification в начало топика: все события будут прочитаны заново.
// Это единственная запись в Kafka из тестов, она допустима только для проверки повторной доставки.
func seekGroupToStart(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), composeTimeout)
	defer cancel()

	_, err := rpkGroup(ctx, "seek", consumerGroup, "--to", "start")
	require.NoError(t, err)
}
