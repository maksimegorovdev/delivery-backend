package kafkaconsumer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	defaultMaxPollRecords = 100
	defaultRetryBackoff   = time.Second
)

type Handler interface {
	Handle(ctx context.Context, msg Message) error
}

type ErrorHandler func(ctx context.Context, err error)

type Consumer struct {
	client         *kgo.Client
	onError        ErrorHandler
	maxPollRecords int
	retryBackoff   time.Duration
	clientOpts     []kgo.Opt
}

func New(brokers []string, group string, topics []string, opts ...Option) (*Consumer, error) {
	c := &Consumer{
		onError:        func(_ context.Context, _ error) {},
		maxPollRecords: defaultMaxPollRecords,
		retryBackoff:   defaultRetryBackoff,
	}

	for _, opt := range opts {
		opt(c)
	}

	clientOpts := slices.Concat(c.clientOpts, []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
	})

	client, err := kgo.NewClient(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("kafkaconsumer: client: %w", err)
	}

	c.client = client
	return c, nil
}

func (c *Consumer) Run(ctx context.Context, handler Handler) {
	defer c.client.AllowRebalance()

	for {
		fetches := c.client.PollRecords(ctx, c.maxPollRecords)
		if ctx.Err() != nil || fetches.IsClientClosed() {
			return
		}

		if err := fetchError(fetches); err != nil {
			c.onError(ctx, err)
			c.client.AllowRebalance()

			if !c.wait(ctx) {
				return
			}
			continue
		}

		rewound := c.process(ctx, fetches, handler)
		c.client.AllowRebalance()

		if rewound && !c.wait(ctx) {
			return
		}
	}
}

// fetchError собирает ошибки фетча в одну; nil, если ошибок нет.
func fetchError(fetches kgo.Fetches) error {
	var errs []error
	fetches.EachError(func(topic string, partition int32, err error) {
		errs = append(errs, fmt.Errorf("topic=%s, partition=%d: %w", topic, partition, err))
	})
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("kafkaconsumer: fetch: %w", errors.Join(errs...))
}

// process обрабатывает записи, коммитит успешные и откатывает оффсеты партиций с ошибкой.
// Возвращает true, если хотя бы одна партиция была откачена.
func (c *Consumer) process(ctx context.Context, fetches kgo.Fetches, handler Handler) bool {
	var (
		processed []*kgo.Record
		rewind    = make(map[string]map[int32]kgo.EpochOffset)
	)

	fetches.EachPartition(func(partition kgo.FetchTopicPartition) {
		for _, record := range partition.Records {
			if err := handler.Handle(ctx, toMessage(record)); err != nil {
				if _, ok := rewind[record.Topic]; !ok {
					rewind[record.Topic] = make(map[int32]kgo.EpochOffset)
				}
				rewind[record.Topic][record.Partition] = kgo.EpochOffset{
					Epoch:  record.LeaderEpoch,
					Offset: record.Offset,
				}
				return
			}
			processed = append(processed, record)
		}
	})

	if len(processed) > 0 {
		if err := c.client.CommitRecords(context.WithoutCancel(ctx), processed...); err != nil {
			c.onError(ctx, fmt.Errorf("kafkaconsumer: commit: %w", err))
		}
	}

	if len(rewind) > 0 {
		c.client.SetOffsets(rewind)
	}

	return len(rewind) > 0
}

// wait ждёт retryBackoff; false, если контекст отменён раньше.
func (c *Consumer) wait(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(c.retryBackoff):
		return true
	}
}

func (c *Consumer) Close() {
	c.client.CloseAllowingRebalance()
}
