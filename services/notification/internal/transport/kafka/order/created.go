package order

import (
	"context"
	"fmt"
	"uuid"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/kafka/kafkaconsumer"
	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/service"
)

func (h *Handler) handleOrderCreated(ctx context.Context, msg kafkaconsumer.Message) error {
	var pb orderv1.OrderCreated
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(msg.Value, &pb); err != nil {
		return apperr.InvalidArgument().Wrap(err)
	}
	if err := validateOrderCreated(&pb); err != nil {
		return apperr.InvalidArgument().Wrap(err)
	}

	return h.notification.CreateNotification(
		ctx,
		service.CreateNotificationInput{
			Event: domain.InboxEvent{
				EventID:     pb.GetEventId(),
				Topic:       msg.Topic,
				Partition:   msg.Partition,
				Offset:      msg.Offset,
				EventType:   domain.EventTypeOrderCreated,
				AggregateID: pb.GetOrderId(),
			},
			Payload:  msg.Value,
			Channels: []domain.Channel{domain.ChannelLog},
		},
	)
}

// validateOrderCreated проверяет идентификаторы, которые попадают в UUID-колонки inbox.
// Без проверки `{}` или `null` проходят разбор, падают только на INSERT и ретраятся бесконечно.
func validateOrderCreated(pb *orderv1.OrderCreated) error {
	if _, err := uuid.Parse(pb.GetEventId()); err != nil {
		return fmt.Errorf("event_id: %w", err)
	}
	if _, err := uuid.Parse(pb.GetOrderId()); err != nil {
		return fmt.Errorf("order_id: %w", err)
	}

	return nil
}
