package pg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

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
	if _, err := postgres.DB(ctx, r.pool).Exec(
		ctx,
		`INSERT INTO outbox_events (id, aggregate_type, aggregate_id, type, payload, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
		event.ID, event.AggregateType, event.AggregateID, event.Type, event.Payload, event.CreatedAt,
	); err != nil {
		return pgerr.Map(err)
	}
	return nil
}
