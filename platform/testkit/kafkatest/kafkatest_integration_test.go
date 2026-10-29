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
