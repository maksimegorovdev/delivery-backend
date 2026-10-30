//go:build integration

package kafkatest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/kafkatest"
)

const consumeTimeout = 30 * time.Second

func TestCreateTopic_ProduceAndConsume(t *testing.T) {
	t.Parallel()

	brokers := kafkatest.Brokers(t)
	topic := kafkatest.CreateTopic(t, "orders")

	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	res := client.ProduceSync(t.Context(), &kgo.Record{Topic: topic, Value: []byte("hello")})
	require.NoError(t, res.FirstErr())

	ctx, cancel := context.WithTimeout(t.Context(), consumeTimeout)
	defer cancel()

	fetches := client.PollFetches(ctx)
	require.NoError(t, fetches.Err())

	records := fetches.Records()
	require.Len(t, records, 1)
	assert.Equal(t, "hello", string(records[0].Value))
}

func TestCreateTopic_ParallelTestsGetDifferentTopics(t *testing.T) {
	t.Parallel()

	const topicCount = 2

	topics := make(chan string, topicCount)

	// Cleanup родителя выполняется после завершения всех параллельных подтестов.
	t.Cleanup(func() {
		close(topics)

		unique := make(map[string]struct{}, topicCount)
		for name := range topics {
			unique[name] = struct{}{}
		}
		assert.Len(t, unique, topicCount, "каждый CreateTopic должен возвращать уникальное имя")
	})

	for i := range topicCount {
		t.Run(fmt.Sprintf("topic-%d", i), func(t *testing.T) {
			t.Parallel()

			topics <- kafkatest.CreateTopic(t, "same-name")
		})
	}
}

func TestProduce_DeliversRecordsInOrder(t *testing.T) {
	t.Parallel()

	brokers := kafkatest.Brokers(t)
	topic := kafkatest.CreateTopic(t, "produce")

	kafkatest.Produce(t, topic,
		&kgo.Record{
			Key:     []byte("key-1"),
			Value:   []byte("first"),
			Headers: []kgo.RecordHeader{{Key: "event-type", Value: []byte("order.created")}},
		},
		&kgo.Record{Key: []byte("key-2"), Value: []byte("second")},
		&kgo.Record{Key: []byte("key-3"), Value: []byte("third")},
	)

	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	ctx, cancel := context.WithTimeout(t.Context(), consumeTimeout)
	defer cancel()

	var got []*kgo.Record
	for len(got) < 3 {
		fetches := client.PollFetches(ctx)
		require.NoError(t, fetches.Err())
		got = append(got, fetches.Records()...)
	}

	require.Len(t, got, 3)
	assert.Equal(t, "key-1", string(got[0].Key))
	assert.Equal(t, "first", string(got[0].Value))
	require.Len(t, got[0].Headers, 1)
	assert.Equal(t, "event-type", got[0].Headers[0].Key)
	assert.Equal(t, "order.created", string(got[0].Headers[0].Value))
	assert.Equal(t, "second", string(got[1].Value))
	assert.Equal(t, "third", string(got[2].Value))
}
