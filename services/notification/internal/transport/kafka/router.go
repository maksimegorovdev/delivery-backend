package kafka

import (
	"context"
	"log/slog"

	"github.com/maksimegorovdev/delivery-backend/platform/kafka/kafkaconsumer"
)

type Router struct {
	handlers map[string]kafkaconsumer.Handler
	log      *slog.Logger
}

func NewRouter(log *slog.Logger) *Router {
	return &Router{
		handlers: make(map[string]kafkaconsumer.Handler),
		log:      log,
	}
}

func (r *Router) Register(topic string, handler kafkaconsumer.Handler) {
	r.handlers[topic] = handler
}

func (r *Router) Handle(ctx context.Context, msg kafkaconsumer.Message) error {
	h, ok := r.handlers[msg.Topic]
	if !ok {
		r.log.WarnContext(
			ctx,
			"no handler for topic",
			slog.String("topic", msg.Topic),
		)
		return nil
	}
	return h.Handle(ctx, msg)
}
