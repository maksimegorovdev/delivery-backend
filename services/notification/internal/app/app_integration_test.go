//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/kafkatest"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/app"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka/order"
)

const migrationsDir = "../../migrations/pg"

// contractsDir — каталог эталонов событий в корне репозитория.
const contractsDir = "../../../../contracts/events"

const goldenOrderCreated = "order.created.json"

// appName помечает соединения приложения в pg_stat_activity.
const appName = "notification-app-test"

// consumerGroup уникален в рамках брокера тестового бинаря: другие тесты пакета его не используют.
const consumerGroup = "notification-app-smoke"

const (
	eventuallyTimeout = 30 * time.Second
	eventuallyTick    = 50 * time.Millisecond
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

// createExactTopic создаёт топик с точным именем topic: kafkatest.CreateTopic добавляет суффикс,
// а роутер приложения привязан к константе order.Topic. Автосоздание топиков в Redpanda выключено.
func createExactTopic(t *testing.T, brokers []string, topic string) {
	t.Helper()

	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	topicReq := kmsg.NewCreateTopicsRequestTopic()
	topicReq.Topic = topic
	topicReq.NumPartitions = 1
	topicReq.ReplicationFactor = 1

	req := kmsg.NewCreateTopicsRequest()
	req.Topics = append(req.Topics, topicReq)

	resp, err := req.RequestWith(t.Context(), client)
	require.NoError(t, err)
	require.Len(t, resp.Topics, 1)

	// Брокер общий на бинарь: при -count>1 топик остаётся от предыдущего прогона.
	if topicErr := kerr.ErrorForCode(resp.Topics[0].ErrorCode); !errors.Is(topicErr, kerr.TopicAlreadyExists) {
		require.NoError(t, topicErr)
	}
}

// dsnOf собирает DSN к той же БД, что и пул pgtest: приложению нужна строка подключения, а не пул.
func dsnOf(pool *pgxpool.Pool) string {
	cc := pool.Config().ConnConfig

	u := url.URL{
		Scheme:   "postgresql",
		User:     url.UserPassword(cc.User, cc.Password),
		Host:     net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))),
		Path:     "/" + cc.Database,
		RawQuery: url.Values{"sslmode": {"disable"}, "application_name": {appName}}.Encode(),
	}

	return u.String()
}

// setEnv задаёт окружение приложения; телеметрия выключена.
func setEnv(t *testing.T, dsn string, brokers []string, topic string) {
	t.Helper()

	t.Setenv("APP_NAME", "notification")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("PG_DSN", dsn)
	t.Setenv("PG_MAX_CONNS", "4")
	t.Setenv("KAFKA_BROKERS", strings.Join(brokers, ","))
	t.Setenv("KAFKA_CONSUMER_GROUP", consumerGroup)
	t.Setenv("KAFKA_TOPICS", topic)
	t.Setenv("OTEL_ENABLED", "false")
}

//nolint:paralleltest // t.Setenv
func TestApp_Smoke(t *testing.T) {
	pool := pgtest.NewDB(t, migrationsDir)

	// Роутер приложения привязан к order.Topic, поэтому топик нельзя заменить уникальным:
	// изоляция обеспечивается тем, что брокер принадлежит тестовому бинарю пакета.
	brokers := kafkatest.Brokers(t)
	createExactTopic(t, brokers, order.Topic)
	setEnv(t, dsnOf(pool), brokers, order.Topic)

	a, err := app.New(t.Context())
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(runCtx) }()

	kafkatest.Produce(t, order.Topic, goldenRecord(t))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var n int
		queryErr := pool.QueryRow(t.Context(), `SELECT count(*) FROM notifications`).Scan(&n)
		if assert.NoError(c, queryErr) {
			assert.Equal(c, 1, n)
		}
	}, eventuallyTimeout, eventuallyTick)

	cancel()

	select {
	case runErr := <-runDone:
		require.NoError(t, runErr)
	case <-time.After(eventuallyTimeout):
		require.Fail(t, "Run did not stop after context cancel")
	}

	require.NoError(t, a.Close(t.Context()))
}
