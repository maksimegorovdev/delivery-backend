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
	eventID, orderID, err := orderCreatedIDs(&pb)
	if err != nil {
		return apperr.InvalidArgument().Wrap(err)
	}

	return h.notification.CreateNotification(
		ctx,
		service.CreateNotificationInput{
			Event: domain.InboxEvent{
				EventID:     eventID,
				Topic:       msg.Topic,
				Partition:   msg.Partition,
				Offset:      msg.Offset,
				EventType:   domain.EventTypeOrderCreated,
				AggregateID: orderID,
			},
			Payload:  msg.Value,
			Channels: []domain.Channel{domain.ChannelLog},
		},
	)
}

// orderCreatedIDs разбирает идентификаторы, которые попадают в UUID-колонки inbox, и
// возвращает их в канонической форме. [uuid.Parse] принимает и urn:uuid:, {…}, запись без
// дефисов, а Postgres отвергает urn:uuid:; каноническая форма к тому же делает event_id
// стабильным ключом идемпотентности.
//
// Проверка переносит отказ с INSERT на разбор, но не убирает ретрай: консьюмер откатывает
// оффсет при любой ошибке, и невалидное сообщение по-прежнему блокирует партицию.
func orderCreatedIDs(pb *orderv1.OrderCreated) (string, string, error) {
	eventUUID, err := uuid.Parse(pb.GetEventId())
	if err != nil {
		return "", "", fmt.Errorf("event_id: %w", err)
	}
	orderUUID, err := uuid.Parse(pb.GetOrderId())
	if err != nil {
		return "", "", fmt.Errorf("order_id: %w", err)
	}

	return eventUUID.String(), orderUUID.String(), nil
}
