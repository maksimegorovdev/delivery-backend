//go:build integration

package kafka_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/kafkatest"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka"
)

// minRetries — сколько попыток обработки упавшей записи нужно увидеть, чтобы считать ретрай доказанным.
const minRetries = 2

func TestOrderCreatedConsumer_ProcessesGoldenEvent(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	topic := kafkatest.CreateTopic(t, "order-created")
	runConsumer(t, pool, topic)

	golden := goldenRecord(t)
	kafkatest.Produce(t, topic, golden)

	eventually(t, func(c *assert.CollectT) {
		n, err := count(t.Context(), pool, countNotificationsQuery)
		if assert.NoError(c, err) {
			assert.Equal(c, 1, n)
		}
	})

	requireCount(t, pool, 1, countInboxQuery)

	var inbox domain.InboxEvent
	require.NoError(t, pool.QueryRow(
		t.Context(),
		`SELECT event_id::text, topic, partition, msg_offset, event_type, aggregate_id::text FROM inbox_events`,
	).Scan(&inbox.EventID, &inbox.Topic, &inbox.Partition, &inbox.Offset, &inbox.EventType, &inbox.AggregateID))
	assert.Equal(t, domain.InboxEvent{
		EventID:     goldenEventID,
		Topic:       topic,
		Partition:   0,
		Offset:      0,
		EventType:   domain.EventTypeOrderCreated,
		AggregateID: goldenOrderID,
	}, inbox)

	var (
		eventID, eventType, channel, status string
		payload                             []byte
	)
	require.NoError(t, pool.QueryRow(
		t.Context(),
		`SELECT event_id::text, event_type, channel, status, payload FROM notifications`,
	).Scan(&eventID, &eventType, &channel, &status, &payload))
	assert.Equal(t, goldenEventID, eventID)
	assert.Equal(t, domain.EventTypeOrderCreated, eventType)
	assert.Equal(t, string(domain.ChannelLog), channel)
	assert.Equal(t, "pending", status)
	assert.JSONEq(t, string(golden.Value), string(payload))
}

func TestOrderCreatedConsumer_RedeliveryDoesNotDuplicate(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	topic := kafkatest.CreateTopic(t, "order-created-redelivery")
	probe := runConsumer(t, pool, topic)

	golden := goldenRecord(t)
	// Сентинель идёт в той же партиции после дубля: когда он обработан, дубль гарантированно уже прошёл.
	sentinel := withEventID(t, golden, otherEventID)
	duplicate := &kgo.Record{Key: golden.Key, Headers: golden.Headers, Value: golden.Value}
	kafkatest.Produce(t, topic, golden, duplicate, sentinel)

	eventually(t, func(c *assert.CollectT) {
		n, err := count(t.Context(), pool, `SELECT count(*) FROM notifications WHERE event_id = $1`, otherEventID)
		if assert.NoError(c, err) {
			assert.Equal(c, 1, n)
		}
	})

	requireCount(t, pool, 2, countInboxQuery)
	requireCount(t, pool, 2, countNotificationsQuery)
	requireCount(t, pool, 1, `SELECT count(*) FROM notifications WHERE event_id = $1`, goldenEventID)

	// Дубль дошёл до хендлера и обработан как успешный, а не отброшен и не ретраится.
	for offset := range int64(3) {
		assert.Equal(t, 1, probe.count(offset), "offset %d", offset)
	}
}

func TestOrderCreatedConsumer_UnknownEventTypeDoesNotBlockPartition(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	topic := kafkatest.CreateTopic(t, "order-created-unknown")
	probe := runConsumer(t, pool, topic)

	golden := goldenRecord(t)
	unknown := &kgo.Record{
		Key:     golden.Key,
		Headers: []kgo.RecordHeader{{Key: kafka.HeaderEventType, Value: []byte("OrderCancelled")}},
		Value:   []byte(`{}`),
	}
	kafkatest.Produce(t, topic, unknown, golden)

	eventually(t, func(c *assert.CollectT) {
		n, err := count(t.Context(), pool, countNotificationsQuery)
		if assert.NoError(c, err) {
			assert.Equal(c, 1, n)
		}
	})

	requireCount(t, pool, 1, countInboxQuery)
	assert.Equal(t, 1, probe.count(0), "неизвестный тип обрабатывается один раз и коммитится")
	assert.Equal(t, 1, probe.count(1))
}

// TestOrderCreatedConsumer_InvalidMessageIsRetriedAndBlocksPartition фиксирует текущее поведение:
// невалидное сообщение не коммитится, ретраится и блокирует партицию (см. orderCreatedIDs).
// При появлении DLQ или пропуска «ядовитых» сообщений тест нужно менять вместе с контрактом.
func TestOrderCreatedConsumer_InvalidMessageIsRetriedAndBlocksPartition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value func(t *testing.T, golden *kgo.Record) []byte
	}{
		{
			name:  "not json",
			value: func(*testing.T, *kgo.Record) []byte { return []byte("not-json") },
		},
		{
			name: "event id is not uuid",
			value: func(t *testing.T, golden *kgo.Record) []byte {
				t.Helper()

				return withEventID(t, golden, "not-a-uuid").Value
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pool := pgtest.NewDB(t, migrationsDir)
			topic := kafkatest.CreateTopic(t, "order-created-invalid")
			probe := runConsumer(t, pool, topic)

			golden := goldenRecord(t)
			invalid := &kgo.Record{Key: golden.Key, Headers: golden.Headers, Value: tt.value(t, golden)}
			kafkatest.Produce(t, topic, invalid, golden)

			eventually(t, func(c *assert.CollectT) {
				assert.GreaterOrEqual(c, probe.count(0), minRetries)
			})

			// Обработка последовательна: пока запись 0 падает, запись 1 до хендлера не доходит.
			assert.Zero(t, probe.count(1))
			requireCount(t, pool, 0, countInboxQuery)
			requireCount(t, pool, 0, countNotificationsQuery)
		})
	}
}
