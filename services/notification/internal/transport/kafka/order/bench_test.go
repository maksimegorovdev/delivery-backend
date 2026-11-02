package order_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/maksimegorovdev/delivery-backend/platform/kafka/kafkaconsumer"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka/order"
)

// contractPath — эталон события из contracts/events относительно пакета.
const contractPath = "../../../../../../contracts/events/order.created.json"

// noopNotifications не делает ничего: бенчмарк измеряет разбор payload, а не сервис.
type noopNotifications struct{}

func (noopNotifications) CreateNotification(context.Context, service.CreateNotificationInput) error {
	return nil
}

// contractPayload читает поле value эталонного события.
func contractPayload(b *testing.B) []byte {
	b.Helper()

	raw, err := os.ReadFile(contractPath)
	if err != nil {
		b.Fatal(err)
	}
	var envelope struct {
		Value json.RawMessage `json:"value"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		b.Fatal(err)
	}

	return envelope.Value
}

func BenchmarkHandler_Handle_OrderCreated(b *testing.B) {
	h := order.NewHandler(order.HandlerDeps{
		NotificationService: noopNotifications{},
		Log:                 slog.New(slog.DiscardHandler),
	})
	msg := kafkaconsumer.Message{
		Topic:     order.Topic,
		Partition: 2,
		Offset:    42,
		Value:     contractPayload(b),
		Headers:   map[string]string{kafka.HeaderEventType: domain.EventTypeOrderCreated},
	}
	ctx := b.Context()

	b.ReportAllocs()
	for b.Loop() {
		if err := h.Handle(ctx, msg); err != nil {
			b.Fatal(err)
		}
	}
}
