package pg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/pgerr"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
)

type notificationRow struct {
	ID        string `db:"id"`
	EventID   string `db:"event_id"`
	EventType string `db:"event_type"`
	Channel   string `db:"channel"`
	Payload   []byte `db:"payload"`
	Attempts  int    `db:"attempts"`
}

func (r notificationRow) toDomain() domain.Notification {
	return domain.Notification{
		ID:        r.ID,
		EventID:   r.EventID,
		EventType: r.EventType,
		Channel:   domain.Channel(r.Channel),
		Payload:   r.Payload,
		Attempts:  r.Attempts,
	}
}

type NotificationRepo struct {
	pool *pgxpool.Pool
}

func NewNotificationRepo(pool *pgxpool.Pool) *NotificationRepo {
	return &NotificationRepo{pool: pool}
}

func (r *NotificationRepo) CreateForChannels(ctx context.Context, notification domain.Notification, channels []domain.Channel) error {
	if len(channels) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, ch := range channels {
		batch.Queue(
			`INSERT INTO notifications (event_id, event_type, channel, payload)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (event_id, channel) DO NOTHING`,
			notification.EventID, notification.EventType, ch, notification.Payload,
		)
	}

	results := postgres.DB(ctx, r.pool).SendBatch(ctx, batch)
	defer results.Close()

	for range channels {
		if _, err := results.Exec(); err != nil {
			return pgerr.Map(err)
		}
	}
	return nil
}
