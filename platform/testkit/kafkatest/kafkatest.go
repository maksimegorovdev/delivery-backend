// Package kafkatest даёт интеграционным тестам брокер Redpanda и изолированные топики.
//
// На тестовый бинарь запускается один контейнер. Тесты изолируются уникальными именами топиков,
// поэтому их можно запускать параллельно. Пакет импортируется только из *_test.go.
package kafkatest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redpanda"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

const (
	// Образ совпадает с docker-compose.yml.
	image = "redpandadata/redpanda:v26.2.3"

	startTimeout = 2 * time.Minute
	opTimeout    = 30 * time.Second

	// suffixLen — длина случайного суффикса топика. 8 символов base32 хватает для уникальности в рамках прогона.
	suffixLen = 8
)

//nolint:gochecknoglobals // один контейнер на тестовый бинарь, иначе каждый тест платит за его запуск
var (
	once      sync.Once
	broker    string
	errShared error
)

// Brokers возвращает адреса брокеров для kgo.SeedBrokers. Контейнер запускается при первом вызове.
//
// Если Docker недоступен, тест падает, а не пропускается.
func Brokers(tb testing.TB) []string {
	tb.Helper()

	addr, err := sharedBroker()
	if err != nil {
		tb.Fatalf("kafkatest: %v", err)
	}

	return []string{addr}
}

// CreateTopic создаёт топик с одной партицией и возвращает его имя: name плюс уникальный суффикс.
//
// Топик остаётся до остановки контейнера, который удаляет Ryuk после завершения тестового бинаря.
func CreateTopic(tb testing.TB, name string) string {
	tb.Helper()

	brokers := Brokers(tb)
	topic := name + "-" + strings.ToLower(rand.Text())[:suffixLen]

	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		tb.Fatalf("kafkatest: create client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(tb.Context(), opTimeout)
	defer cancel()

	if err = createTopic(ctx, client, topic); err != nil {
		tb.Fatalf("kafkatest: %v", err)
	}

	return topic
}

// Produce синхронно публикует records в topic и падает при любой ошибке брокера.
//
// Topic каждой записи перезаписывается значением topic. Порядок записей одной партиции сохраняется.
func Produce(tb testing.TB, topic string, records ...*kgo.Record) {
	tb.Helper()

	client, err := kgo.NewClient(kgo.SeedBrokers(Brokers(tb)...))
	if err != nil {
		tb.Fatalf("kafkatest: create client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(tb.Context(), opTimeout)
	defer cancel()

	for _, rec := range records {
		rec.Topic = topic
	}

	if err = client.ProduceSync(ctx, records...).FirstErr(); err != nil {
		tb.Fatalf("kafkatest: produce to %q: %v", topic, err)
	}
}

// sharedBroker запускает контейнер при первом вызове и возвращает его адрес всем последующим.
func sharedBroker() (string, error) {
	once.Do(func() {
		broker, errShared = start()
	})

	return broker, errShared
}

// start поднимает контейнер Redpanda. Контейнер удаляет Ryuk после завершения тестового бинаря.
func start() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), startTimeout)
	defer cancel()

	ctr, err := redpanda.Run(ctx, image)
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr)

		return "", fmt.Errorf("run %s container (is docker running?): %w", image, err)
	}

	addr, err := ctr.KafkaSeedBroker(ctx)
	if err != nil {
		_ = testcontainers.TerminateContainer(ctr)

		return "", fmt.Errorf("get kafka seed broker: %w", err)
	}

	return addr, nil
}

func createTopic(ctx context.Context, client *kgo.Client, topic string) error {
	topicReq := kmsg.NewCreateTopicsRequestTopic()
	topicReq.Topic = topic
	topicReq.NumPartitions = 1
	topicReq.ReplicationFactor = 1

	req := kmsg.NewCreateTopicsRequest()
	req.Topics = append(req.Topics, topicReq)

	resp, err := req.RequestWith(ctx, client)
	if err != nil {
		return fmt.Errorf("create topic %q: %w", topic, err)
	}

	var errs []error
	for _, res := range resp.Topics {
		if kerrErr := kerr.ErrorForCode(res.ErrorCode); kerrErr != nil {
			errs = append(errs, fmt.Errorf("create topic %q: %w", res.Topic, kerrErr))
		}
	}

	return errors.Join(errs...)
}
