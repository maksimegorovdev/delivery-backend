package kafkaconsumer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	defaultMaxPollRecords = 100
	defaultRetryBackoff   = time.Second
)

type Handler interface {
	Handle(ctx context.Context, rec *kgo.Record) error
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
		onError:        func(ctx context.Context, err error) {},
		maxPollRecords: defaultMaxPollRecords,
		retryBackoff:   defaultRetryBackoff,
	}

	for _, opt := range opts {
		opt(c)
	}

	clientOpts := append(c.clientOpts,
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
	)

	client, err := kgo.NewClient(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("kafkaconsumer: client: %w", err)
	}

	c.client = client
	return c, nil
}

func (c *Consumer) Run(ctx context.Context, handler Handler) error {
	defer c.client.AllowRebalance()

	for {
		fetches := c.client.PollRecords(ctx, c.maxPollRecords)
		if ctx.Err() != nil || fetches.IsClientClosed() {
			return nil
		}

		var fetchErrs []error
		fetches.EachError(func(topic string, partition int32, err error) {
			fetchErrs = append(fetchErrs, fmt.Errorf("topic=%s, partition=%d: %w", topic, partition, err))
		})
		if len(fetchErrs) > 0 {
			return fmt.Errorf("kafkaconsumer: fetch: %w", errors.Join(fetchErrs...))
		}

		var (
			processed []*kgo.Record
			rewind    = make(map[string]map[int32]kgo.EpochOffset)
		)

		fetches.EachPartition(func(partition kgo.FetchTopicPartition) {
			for _, record := range partition.Records {
				if err := handler.Handle(ctx, record); err != nil {
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

		c.client.AllowRebalance()

		if len(rewind) > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(c.retryBackoff):
			}
		}
	}
}

func (c *Consumer) Close() {
	c.client.CloseAllowingRebalance()
}
