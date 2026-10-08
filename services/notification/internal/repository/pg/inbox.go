package pg

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/pgerr"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
)

type InboxRepo struct {
	pool *pgxpool.Pool
}

func NewInboxRepo(pool *pgxpool.Pool) *InboxRepo {
	return &InboxRepo{pool: pool}
}

func (r *InboxRepo) Claim(ctx context.Context, event domain.InboxEvent) error {
	if _, err := postgres.DB(ctx, r.pool).Exec(
		ctx,
		`INSERT INTO inbox_events (event_id, topic, partition, msg_offset, event_type, aggregate_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (event_id) DO NOTHING`,
		event.EventID, event.Topic, event.Partition, event.Offset, event.EventType, event.AggregateID,
	); err != nil {
		return pgerr.Map(err)
	}
	return nil
}
