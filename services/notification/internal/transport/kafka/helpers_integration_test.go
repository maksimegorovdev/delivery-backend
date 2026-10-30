//go:build integration

package kafka_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimegorovdev/delivery-backend/platform/kafka/kafkaconsumer"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/kafkatest"

	repo "github.com/maksimegorovdev/delivery-backend/services/notification/internal/repository/pg"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka/order"
)

const migrationsDir = "../../../migrations/pg"

// contractsDir — каталог эталонов событий в корне репозитория.
const contractsDir = "../../../../../contracts/events"

const goldenOrderCreated = "order.created.json"

// retryBackoff мал, чтобы тесты ретрая шли быстро, но заметно больше тика проверки.
const retryBackoff = 200 * time.Millisecond

// Идентификаторы совпадают с эталоном contracts/events/order.created.json.
const (
	goldenEventID = "0190a000-0000-7000-8000-000000000301"
	goldenOrderID = "0190a000-0000-7000-8000-000000000101"
)

// Идентификатор второго события: записи, которое отличается от эталона только event_id.
const otherEventID = "0190a000-0000-7000-8000-000000000302"

const (
	countInboxQuery         = `SELECT count(*) FROM inbox_events`
	countNotificationsQuery = `SELECT count(*) FROM notifications`
)

// goldenMessage — сообщение в Kafka после Debezium EventRouter, как его хранит эталон.
type goldenMessage struct {
	Key     string            `json:"key"`
	Headers map[string]string `json:"headers"`
	Value   json.RawMessage   `json:"value"`
}

// goldenRecord читает эталон order.created из contracts/events и превращает его в запись для публикации.
func goldenRecord(t *testing.T) *kgo.Record {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(contractsDir, goldenOrderCreated))
	require.NoError(t, err)

	var msg goldenMessage
	require.NoError(t, json.Unmarshal(raw, &msg))

	rec := &kgo.Record{Key: []byte(msg.Key), Value: msg.Value}
	for key, value := range msg.Headers {
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: key, Value: []byte(value)})
	}

	return rec
}

// withEventID возвращает копию записи с другим event_id в значении.
func withEventID(t *testing.T, rec *kgo.Record, eventID string) *kgo.Record {
	t.Helper()

	var value map[string]any
	require.NoError(t, json.Unmarshal(rec.Value, &value))
	value["event_id"] = eventID

	raw, err := json.Marshal(value)
	require.NoError(t, err)

	return &kgo.Record{Key: rec.Key, Headers: rec.Headers, Value: raw}
}

// attempts считает попытки обработки по оффсетам и передаёт запись настоящему хендлеру.
//
// Ошибки хендлера Consumer в ErrorHandler не передаёт, поэтому ретраи видны только здесь.
// Топик теста имеет одну партицию, значит оффсет однозначно определяет запись.
type attempts struct {
	next     kafkaconsumer.Handler
	mu       sync.Mutex
	byOffset map[int64]int
}

func (a *attempts) Handle(ctx context.Context, msg kafkaconsumer.Message) error {
	a.mu.Lock()
	a.byOffset[msg.Offset]++
	a.mu.Unlock()

	return a.next.Handle(ctx, msg)
}

// count возвращает число попыток обработки записи с оффсетом offset.
func (a *attempts) count(offset int64) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.byOffset[offset]
}

// runConsumer поднимает настоящий стек repo → service → router/handler → kafkaconsumer на topic
// и останавливает его в t.Cleanup. Группа уникальна, потому что уникален топик.
//
// Вызывать нужно после pgtest.NewDB: cleanup'ы идут в обратном порядке, и consumer
// должен остановиться раньше, чем закроется пул.
func runConsumer(t *testing.T, pool *pgxpool.Pool, topic string) *attempts {
	t.Helper()

	log := slog.New(slog.DiscardHandler)

	svc := service.NewNotificationService(service.NotificationServiceDeps{
		Tx:            postgres.NewTxManager(pool),
		Inbox:         repo.NewInboxRepo(pool),
		Notifications: repo.NewNotificationRepo(pool),
	})

	router := kafka.NewRouter(log)
	router.Register(topic, order.NewHandler(order.HandlerDeps{NotificationService: svc, Log: log}))

	probe := &attempts{next: router, byOffset: make(map[int64]int)}

	cons, err := kafkaconsumer.New(
		kafkatest.Brokers(t),
		topic+"-group",
		[]string{topic},
		kafkaconsumer.WithRetryBackoff(retryBackoff),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)
		cons.Run(ctx, probe)
	}()

	t.Cleanup(func() {
		cancel()

		select {
		case <-done:
			cons.Close()
		case <-time.After(eventuallyTimeout):
			t.Error("consumer did not stop after context cancel")
		}
	})

	return probe
}

// eventually повторяет check, пока все его проверки не пройдут, но не дольше eventuallyTimeout.
func eventually(t *testing.T, check func(c *assert.CollectT)) {
	t.Helper()

	require.EventuallyWithT(t, check, eventuallyTimeout, eventuallyTick)
}

// count выполняет запрос вида SELECT count(*).
func count(ctx context.Context, pool *pgxpool.Pool, query string, args ...any) (int, error) {
	var n int
	err := pool.QueryRow(ctx, query, args...).Scan(&n)

	return n, err
}

// requireCount проверяет число строк сразу, без ожидания.
func requireCount(t *testing.T, pool *pgxpool.Pool, want int, query string, args ...any) {
	t.Helper()

	got, err := count(t.Context(), pool, query, args...)
	require.NoError(t, err)
	require.Equal(t, want, got)
}
