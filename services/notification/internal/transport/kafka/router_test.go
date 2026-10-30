package kafka_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/kafka/kafkaconsumer"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka"
)

type fakeHandler struct {
	got   []kafkaconsumer.Message
	err   error
	calls int
}

func (f *fakeHandler) Handle(_ context.Context, msg kafkaconsumer.Message) error {
	f.calls++
	f.got = append(f.got, msg)
	return f.err
}

func TestRouter_Handle(t *testing.T) {
	t.Parallel()

	errHandler := errors.New("handler failed")

	tests := []struct {
		name      string
		topic     string
		handlers  map[string]*fakeHandler
		wantCalls map[string]int
		wantErr   error
	}{
		{
			name:      "routes message to the handler of its topic",
			topic:     "orders",
			handlers:  map[string]*fakeHandler{"orders": {}, "users": {}},
			wantCalls: map[string]int{"orders": 1, "users": 0},
		},
		{
			name:      "propagates handler error",
			topic:     "orders",
			handlers:  map[string]*fakeHandler{"orders": {err: errHandler}},
			wantCalls: map[string]int{"orders": 1},
			wantErr:   errHandler,
		},
		{
			name:      "unknown topic is skipped without error",
			topic:     "unknown",
			handlers:  map[string]*fakeHandler{"orders": {}},
			wantCalls: map[string]int{"orders": 0},
		},
		{
			name:      "no handlers registered",
			topic:     "orders",
			handlers:  map[string]*fakeHandler{},
			wantCalls: map[string]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			router := kafka.NewRouter(slog.New(slog.DiscardHandler))
			for topic, h := range tt.handlers {
				router.Register(topic, h)
			}
			msg := kafkaconsumer.Message{Topic: tt.topic, Partition: 3, Offset: 7, Value: []byte("v")}

			err := router.Handle(t.Context(), msg)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			for topic, want := range tt.wantCalls {
				assert.Equal(t, want, tt.handlers[topic].calls, "calls of %q handler", topic)
			}
			if h, ok := tt.handlers[tt.topic]; ok && tt.wantCalls[tt.topic] == 1 {
				assert.Equal(t, []kafkaconsumer.Message{msg}, h.got)
			}
		})
	}
}

func TestRouter_Register_ReplacesHandler(t *testing.T) {
	t.Parallel()

	first, second := &fakeHandler{}, &fakeHandler{}
	router := kafka.NewRouter(slog.New(slog.DiscardHandler))
	router.Register("orders", first)
	router.Register("orders", second)

	require.NoError(t, router.Handle(t.Context(), kafkaconsumer.Message{Topic: "orders"}))

	assert.Zero(t, first.calls)
	assert.Equal(t, 1, second.calls)
}

func TestRouter_Handle_UnknownTopicLogsWarning(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	router := kafka.NewRouter(slog.New(slog.NewTextHandler(&buf, nil)))

	require.NoError(t, router.Handle(t.Context(), kafkaconsumer.Message{Topic: "unknown"}))

	assert.Contains(t, buf.String(), "level=WARN")
	assert.Contains(t, buf.String(), "topic=unknown")
}
