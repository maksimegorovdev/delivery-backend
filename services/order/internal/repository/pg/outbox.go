package pg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/pgerr"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

type OutboxRepo struct {
	pool *pgxpool.Pool
}

func NewOutboxRepo(pool *pgxpool.Pool) *OutboxRepo {
	return &OutboxRepo{pool: pool}
}

func (r *OutboxRepo) Save(ctx context.Context, event domain.OutboxEvent) error {
	traceparent, tracestate := traceContext(ctx, otel.GetTextMapPropagator())

	if _, err := postgres.DB(ctx, r.pool).Exec(
		ctx,
		`INSERT INTO outbox_events (id, aggregate_type, aggregate_id, type, payload, created_at, traceparent, tracestate)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		event.ID, event.AggregateType, event.AggregateID, event.Type, event.Payload, event.CreatedAt,
		traceparent, tracestate,
	); err != nil {
		return pgerr.Map(err)
	}
	return nil
}

// traceContext возвращает W3C trace context активного span'а из ctx для колонок
// traceparent/tracestate (в таком порядке). Отсутствующее значение превращается в nil (SQL NULL).
func traceContext(ctx context.Context, propagator propagation.TextMapPropagator) (*string, *string) {
	carrier := propagation.MapCarrier{}
	propagator.Inject(ctx, carrier)

	return nilIfEmpty(carrier.Get("traceparent")), nilIfEmpty(carrier.Get("tracestate"))
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
