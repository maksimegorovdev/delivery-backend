package order

import (
	"context"
	"log/slog"

	"github.com/maksimegorovdev/delivery-backend/platform/kafka/kafkaconsumer"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka"
)

const Topic = "order.events.v1"

type NotificationService interface {
	CreateNotification(ctx context.Context, input service.CreateNotificationInput) error
}

type HandlerDeps struct {
	NotificationService NotificationService
	Log                 *slog.Logger
}

type Handler struct {
	notification NotificationService
	log          *slog.Logger
}

func NewHandler(deps HandlerDeps) *Handler {
	return &Handler{
		notification: deps.NotificationService,
		log:          deps.Log,
	}
}

func (h *Handler) Handle(ctx context.Context, msg kafkaconsumer.Message) error {
	eventType := msg.Headers[kafka.HeaderEventType]

	if err := h.route(ctx, msg, eventType); err != nil {
		h.log.ErrorContext(
			ctx,
			"handle message",
			slog.String("topic", msg.Topic),
			slog.Int("partition", int(msg.Partition)),
			slog.Int64("offset", msg.Offset),
			slog.String("event_type", eventType),
			logger.Err(err),
		)
		return err
	}
	return nil
}

func (h *Handler) route(ctx context.Context, msg kafkaconsumer.Message, eventType string) error {
	switch eventType {
	case domain.EventTypeOrderCreated:
		return h.handleOrderCreated(ctx, msg)
	default:
		return nil
	}
}
