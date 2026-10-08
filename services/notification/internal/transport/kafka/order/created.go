package order

import (
	"context"

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
