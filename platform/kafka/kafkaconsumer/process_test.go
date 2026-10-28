//nolint:testpackage // тест использует неэкспортируемые символы пакета
package kafkaconsumer

import (
	"context"
	"errors"
	"testing"

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

func attrValue(attrs []attribute.KeyValue, key attribute.Key) (attribute.Value, bool) {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return attribute.Value{}, false
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
	if err != nil {
		t.Fatalf("processRecord() error = %v, want nil", err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	span := spans[0]

	if span.SpanKind != trace.SpanKindConsumer {
		t.Errorf("SpanKind = %v, want Consumer", span.SpanKind)
	}
	if span.Status.Code == codes.Error {
		t.Errorf("Status = Error, want не Error")
	}
	if v, ok := attrValue(span.Attributes, "messaging.source.name"); !ok || v.AsString() != "order.events.v1" {
		t.Errorf("messaging.source.name = %v (found=%v), want order.events.v1", v, ok)
	}
	if handlerSpan.SpanID() != span.SpanContext.SpanID() {
		t.Errorf("спан в ctx хендлера = %s, want спан process %s", handlerSpan.SpanID(), span.SpanContext.SpanID())
	}
}

func TestProcessRecord_HandlerErrorMarksSpan(t *testing.T) {
	t.Parallel()

	c, exporter := newTestConsumer(t)
	record := &kgo.Record{Topic: "order.events.v1"}
	wantErr := errors.New("boom: email=user@example.com")

	err := c.processRecord(context.Background(), record, handlerFunc(func(context.Context, Message) error {
		return wantErr
	}))
	if !errors.Is(err, wantErr) {
		t.Fatalf("processRecord() error = %v, want %v", err, wantErr)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans = %d, want 1", len(spans))
	}
	span := spans[0]
	if span.Status.Code != codes.Error || span.Status.Description != handlerFailed {
		t.Errorf("Status = %v %q, want Error %q", span.Status.Code, span.Status.Description, handlerFailed)
	}
	if v, ok := attrValue(span.Attributes, errorTypeKey); !ok || v.AsString() != "*errors.errorString" {
		t.Errorf("%s = %v (found=%v), want *errors.errorString", errorTypeKey, v, ok)
	}
	// Текст ошибки может нести данные из payload и не должен попадать в спан.
	if len(span.Events) != 0 {
		t.Errorf("Events = %v, want нет событий с текстом ошибки", span.Events)
	}
}

func TestProcessRecord_KeepsParentContextCancellation(t *testing.T) {
	t.Parallel()

	c, _ := newTestConsumer(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = c.processRecord(ctx, &kgo.Record{Topic: "t"}, handlerFunc(func(ctx context.Context, _ Message) error {
		if ctx.Err() == nil {
			t.Error("ctx хендлера не отменён, хотя родительский отменён")
		}
		return nil
	}))
}
