//nolint:testpackage // the test uses unexported package symbols
package kafkaconsumer

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/plugin/kotel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type handlerFunc func(ctx context.Context, msg Message) error

func (f handlerFunc) Handle(ctx context.Context, msg Message) error { return f(ctx, msg) }

func newTestConsumer(t *testing.T) (*Consumer, *tracetest.InMemoryExporter) {
	t.Helper()

	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	return &Consumer{tracer: kotel.NewTracer(kotel.TracerProvider(tp))}, exporter
}

// newTestConsumerWithClient adds a kgo client without a group or a broker: no network is needed until reading
// starts, and a commit on such a client returns an error that is passed to onError.
func newTestConsumerWithClient(t *testing.T) (*Consumer, *[]error) {
	t.Helper()

	c, _ := newTestConsumer(t)

	client, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:1"))
	require.NoError(t, err)
	t.Cleanup(client.Close)

	var reported []error
	c.client = client
	c.onError = func(_ context.Context, err error) { reported = append(reported, err) }

	return c, &reported
}

func attrValue(attrs []attribute.KeyValue, key attribute.Key) (attribute.Value, bool) {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
}

func fetchOf(partitions ...kgo.FetchPartition) kgo.Fetches {
	return kgo.Fetches{{Topics: []kgo.FetchTopic{{Topic: "order.events.v1", Partitions: partitions}}}}
}

func partitionOf(partition int32, offsets ...int64) kgo.FetchPartition {
	records := make([]*kgo.Record, 0, len(offsets))
	for _, offset := range offsets {
		records = append(records, &kgo.Record{Topic: "order.events.v1", Partition: partition, Offset: offset})
	}

	return kgo.FetchPartition{Partition: partition, Records: records}
}

func TestProcessRecord_CreatesProcessSpanPerRecord(t *testing.T) {
	t.Parallel()

	c, exporter := newTestConsumer(t)
	record := &kgo.Record{Topic: "order.events.v1", Partition: 2, Offset: 7}

	var handlerSpan trace.SpanContext
	err := c.processRecord(context.Background(), record, handlerFunc(func(ctx context.Context, _ Message) error {
		handlerSpan = trace.SpanContextFromContext(ctx)
		return nil
	}))
	require.NoError(t, err)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	span := spans[0]

	assert.Equal(t, trace.SpanKindConsumer, span.SpanKind)
	assert.NotEqual(t, codes.Error, span.Status.Code)
	v, ok := attrValue(span.Attributes, "messaging.source.name")
	assert.True(t, ok, "messaging.source.name not found")
	assert.Equal(t, "order.events.v1", v.AsString())
	assert.Equal(t, span.SpanContext.SpanID(), handlerSpan.SpanID(),
		"the span in the handler ctx must be the process span")
}

func TestProcessRecord_HandlerErrorMarksSpan(t *testing.T) {
	t.Parallel()

	c, exporter := newTestConsumer(t)
	record := &kgo.Record{Topic: "order.events.v1"}
	wantErr := errors.New("boom: email=user@example.com")

	err := c.processRecord(context.Background(), record, handlerFunc(func(context.Context, Message) error {
		return wantErr
	}))
	require.ErrorIs(t, err, wantErr)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)
	span := spans[0]

	assert.Equal(t, codes.Error, span.Status.Code)
	assert.Equal(t, handlerFailed, span.Status.Description)
	v, ok := attrValue(span.Attributes, errorTypeKey)
	assert.True(t, ok, "%s not found", errorTypeKey)
	assert.Equal(t, "*errors.errorString", v.AsString())
	// The error text may carry payload data and must not end up in the span.
	assert.Empty(t, span.Events, "the span must have no events carrying the error text")
}

func TestProcessRecord_KeepsParentContextCancellation(t *testing.T) {
	t.Parallel()

	c, _ := newTestConsumer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = c.processRecord(ctx, &kgo.Record{Topic: "t"}, handlerFunc(func(ctx context.Context, _ Message) error {
		assert.Error(t, ctx.Err(), "handler ctx is not canceled although the parent is")
		return nil
	}))
}

func TestProcessRecord_PassesMessageToHandler(t *testing.T) {
	t.Parallel()

	c, _ := newTestConsumer(t)
	record := &kgo.Record{
		Topic:     "order.events.v1",
		Partition: 3,
		Offset:    42,
		Key:       []byte("key"),
		Value:     []byte("value"),
		Headers:   []kgo.RecordHeader{{Key: "h", Value: []byte("v")}},
	}

	var got Message
	err := c.processRecord(context.Background(), record, handlerFunc(func(_ context.Context, msg Message) error {
		got = msg
		return nil
	}))
	require.NoError(t, err)

	assert.Equal(t, toMessage(record), got)
}

func TestConsumer_process_CommitsAllRecordsOnSuccess(t *testing.T) {
	t.Parallel()

	c, reported := newTestConsumerWithClient(t)

	var handled []int64
	rewound := c.process(context.Background(), fetchOf(partitionOf(0, 1, 2, 3)),
		handlerFunc(func(_ context.Context, msg Message) error {
			handled = append(handled, msg.Offset)
			return nil
		}))

	assert.False(t, rewound, "no offset rewind is needed without handler errors")
	assert.Equal(t, []int64{1, 2, 3}, handled)
	// A client without a group cannot commit: the commit error is passed to onError, not lost.
	require.Len(t, *reported, 1)
	assert.ErrorContains(t, (*reported)[0], "kafkaconsumer: commit")
}

func TestConsumer_process_HandlerErrorStopsPartitionAndRewinds(t *testing.T) {
	t.Parallel()

	c, reported := newTestConsumerWithClient(t)

	failOn := errors.New("handler failed")
	var handled []string
	rewound := c.process(context.Background(), fetchOf(partitionOf(0, 1, 2, 3), partitionOf(1, 10, 11)),
		handlerFunc(func(_ context.Context, msg Message) error {
			handled = append(handled, fmt.Sprintf("%d:%d", msg.Partition, msg.Offset))
			if msg.Partition == 0 && msg.Offset == 2 {
				return failOn
			}
			return nil
		}))

	assert.True(t, rewound, "a handler error must rewind the partition")
	// After the error at offset 2, offset 3 of the same partition is skipped; the neighbor partition is fully processed.
	assert.Equal(t, []string{"0:1", "0:2", "1:10", "1:11"}, handled)
	// Successful records (0:1, 1:10, 1:11) are committed in a single call.
	require.Len(t, *reported, 1)
	assert.ErrorContains(t, (*reported)[0], "kafkaconsumer: commit")
}

func TestConsumer_process_FirstRecordErrorCommitsNothing(t *testing.T) {
	t.Parallel()

	c, reported := newTestConsumerWithClient(t)

	rewound := c.process(context.Background(), fetchOf(partitionOf(0, 5, 6)),
		handlerFunc(func(context.Context, Message) error { return errors.New("boom") }))

	assert.True(t, rewound)
	assert.Empty(t, *reported, "nothing to commit: commit is not called")
}

func TestConsumer_process_EmptyFetches(t *testing.T) {
	t.Parallel()

	c, reported := newTestConsumerWithClient(t)

	rewound := c.process(context.Background(), kgo.Fetches{},
		handlerFunc(func(context.Context, Message) error {
			t.Error("the handler must not be called without records")
			return nil
		}))

	assert.False(t, rewound)
	assert.Empty(t, *reported)
}

func Test_fetchError(t *testing.T) {
	t.Parallel()

	errBroker := errors.New("broker down")

	t.Run("no errors", func(t *testing.T) {
		t.Parallel()

		assert.NoError(t, fetchError(fetchOf(partitionOf(0, 1))))
	})

	t.Run("collects partition errors", func(t *testing.T) {
		t.Parallel()

		fetches := kgo.Fetches{{Topics: []kgo.FetchTopic{{
			Topic: "orders",
			Partitions: []kgo.FetchPartition{
				{Partition: 4, Err: errBroker},
				{Partition: 5},
			},
		}}}}

		err := fetchError(fetches)

		require.ErrorIs(t, err, errBroker)
		require.ErrorContains(t, err, "topic=orders, partition=4")
		assert.NotContains(t, err.Error(), "partition=5")
	})
}

func TestConsumer_wait(t *testing.T) {
	t.Parallel()

	t.Run("backoff elapsed", func(t *testing.T) {
		t.Parallel()

		c := &Consumer{retryBackoff: time.Millisecond}

		assert.True(t, c.wait(context.Background()))
	})

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		c := &Consumer{retryBackoff: time.Hour}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		assert.False(t, c.wait(ctx))
	})
}

func TestConsumer_Run_ReturnsWhenClientClosed(t *testing.T) {
	t.Parallel()

	c, err := New([]string{"127.0.0.1:1"}, "group", []string{"topic"})
	require.NoError(t, err)
	c.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Run(context.Background(), handlerFunc(func(context.Context, Message) error { return nil }))
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Close")
	}
}
