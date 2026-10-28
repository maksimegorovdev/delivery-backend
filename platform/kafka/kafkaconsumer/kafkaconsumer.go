package kafkaconsumer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
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
	tracer         *kotel.Tracer
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

	// Провайдеры берутся из глобальных otel: при отключённой телеметрии они noop.
	c.tracer = kotel.NewTracer(kotel.ConsumerGroup(group))
	hooks := kotel.NewKotel(
		kotel.WithTracer(c.tracer),
		kotel.WithMeter(kotel.NewMeter()),
	).Hooks()

	clientOpts := slices.Concat(c.clientOpts, []kgo.Opt{
		kgo.WithHooks(hooks...),
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
			if err := c.processRecord(ctx, record, handler); err != nil {
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

// processRecord вызывает handler внутри спана process. Родитель спана — спан receive,
// который kotel создаёт при буферизации записи как продолжение traceparent из заголовков
// (без traceparent receive — корень трейса). Спан кладётся в ctx хендлера поверх ctx
// консьюмера, чтобы отмена и значения вызывающего сохранялись. Ошибка хендлера
// возвращается как есть.
func (c *Consumer) processRecord(ctx context.Context, record *kgo.Record, handler Handler) error {
	_, span := c.tracer.WithProcessSpan(record)
	defer span.End()

	err := handler.Handle(trace.ContextWithSpan(ctx, span), toMessage(record))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	return err
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
