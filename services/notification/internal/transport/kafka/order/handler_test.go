package order_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/kafka/kafkaconsumer"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka/order"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka/order/mocks"
)

func newHandler(t *testing.T) (*order.Handler, *mocks.MockNotificationService) {
	t.Helper()

	svc := mocks.NewMockNotificationService(t)
	h := order.NewHandler(order.HandlerDeps{
		NotificationService: svc,
		Log:                 slog.New(slog.DiscardHandler),
	})
	return h, svc
}

func orderCreatedMessage(value string) kafkaconsumer.Message {
	return kafkaconsumer.Message{
		Topic:     order.Topic,
		Partition: 2,
		Offset:    42,
		Value:     []byte(value),
		Headers:   map[string]string{kafka.HeaderEventType: domain.EventTypeOrderCreated},
	}
}

func TestHandler_Handle_OrderCreated(t *testing.T) {
	t.Parallel()

	const payload = `{"event_id":"event-1","order_id":"order-1","user_id":"user-1","total_amount":430}`

	h, svc := newHandler(t)
	svc.EXPECT().
		CreateNotification(mock.Anything, service.CreateNotificationInput{
			Event: domain.InboxEvent{
				EventID:     "event-1",
				Topic:       order.Topic,
				Partition:   2,
				Offset:      42,
				EventType:   domain.EventTypeOrderCreated,
				AggregateID: "order-1",
			},
			Payload:  []byte(payload),
			Channels: []domain.Channel{domain.ChannelLog},
		}).
		Return(nil).
		Once()

	err := h.Handle(t.Context(), orderCreatedMessage(payload))

	require.NoError(t, err)
}

func TestHandler_Handle_OrderCreated_IgnoresUnknownFields(t *testing.T) {
	t.Parallel()

	const payload = `{"event_id":"event-1","order_id":"order-1","field_from_the_future":{"a":1}}`

	h, svc := newHandler(t)
	svc.EXPECT().
		CreateNotification(mock.Anything, mock.MatchedBy(func(in service.CreateNotificationInput) bool {
			return in.Event.EventID == "event-1" && in.Event.AggregateID == "order-1"
		})).
		Return(nil).
		Once()

	err := h.Handle(t.Context(), orderCreatedMessage(payload))

	require.NoError(t, err)
}

func TestHandler_Handle_OrderCreated_ServiceError(t *testing.T) {
	t.Parallel()

	errService := errors.New("create notification failed")

	h, svc := newHandler(t)
	svc.EXPECT().
		CreateNotification(mock.Anything, mock.Anything).
		Return(errService).
		Once()

	err := h.Handle(t.Context(), orderCreatedMessage(`{"event_id":"event-1","order_id":"order-1"}`))

	require.ErrorIs(t, err, errService)
}

func TestHandler_Handle_InvalidPayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "not json", value: `not a json`},
		{name: "empty value", value: ``},
		{name: "wrong field type", value: `{"event_id":123}`},
		{name: "truncated json", value: `{"event_id":"event-1"`},
		{name: "invalid utf-8", value: "{\"event_id\":\"\xff\xfe\"}"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, _ := newHandler(t) // сервис без ожиданий: любой вызов роняет тест

			err := h.Handle(t.Context(), orderCreatedMessage(tt.value))

			require.Error(t, err)
			appErr, ok := apperr.As(err)
			require.True(t, ok, "want apperr.Error, got %v", err)
			assert.Equal(t, apperr.CodeInvalidArgument, appErr.Code)
		})
	}
}

func TestHandler_Handle_UnknownEventType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers map[string]string
	}{
		{name: "unknown event type", headers: map[string]string{kafka.HeaderEventType: "order.cancelled"}},
		{name: "empty event type", headers: map[string]string{kafka.HeaderEventType: ""}},
		{name: "no event type header", headers: map[string]string{"other": "value"}},
		{name: "nil headers", headers: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h, _ := newHandler(t) // сервис без ожиданий: tolerant reader не должен его вызывать
			msg := orderCreatedMessage(`not a json, but must not even be parsed`)
			msg.Headers = tt.headers

			err := h.Handle(t.Context(), msg)

			require.NoError(t, err)
		})
	}
}

type recordingService struct {
	inputs []service.CreateNotificationInput
}

func (r *recordingService) CreateNotification(_ context.Context, in service.CreateNotificationInput) error {
	r.inputs = append(r.inputs, in)
	return nil
}

func FuzzHandleOrderCreated(f *testing.F) {
	f.Add([]byte(`{"event_id":"event-1","order_id":"order-1","user_id":"user-1","total_amount":430}`))
	f.Add([]byte(`{"event_id":"event-1","order_id":"order-1","field_from_the_future":{"a":1}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(``))
	f.Add([]byte(`null`))
	f.Add([]byte(`not a json`))
	f.Add([]byte(`{"event_id":123}`))
	f.Add([]byte(`{"items":[{"quantity":"x"}]}`))
	f.Add([]byte("{\"event_id\":\"\xff\xfe\"}"))

	f.Fuzz(func(t *testing.T, value []byte) {
		svc := &recordingService{}
		h := order.NewHandler(order.HandlerDeps{
			NotificationService: svc,
			Log:                 slog.New(slog.DiscardHandler),
		})
		msg := orderCreatedMessage("")
		msg.Value = value

		err := h.Handle(context.Background(), msg)

		if err != nil {
			appErr, ok := apperr.As(err)
			require.True(t, ok, "want apperr.Error, got %v", err)
			require.Equal(t, apperr.CodeInvalidArgument, appErr.Code)
			require.Empty(t, svc.inputs, "service must not be called on a malformed payload")
			return
		}
		require.Len(t, svc.inputs, 1)
		require.Equal(t, value, svc.inputs[0].Payload)
		require.Equal(t, domain.EventTypeOrderCreated, svc.inputs[0].Event.EventType)
		require.Equal(t, msg.Topic, svc.inputs[0].Event.Topic)
		require.Equal(t, msg.Partition, svc.inputs[0].Event.Partition)
		require.Equal(t, msg.Offset, svc.inputs[0].Event.Offset)
	})
}
