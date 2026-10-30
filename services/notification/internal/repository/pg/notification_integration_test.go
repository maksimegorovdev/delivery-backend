//go:build integration

package pg_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/repository/pg"
)

func fixtureNotification() domain.Notification {
	return domain.Notification{
		EventID:   fixtureEventID,
		EventType: "order.created",
		Payload:   []byte(`{"order_id":"` + fixtureAggregateID + `"}`),
	}
}

func TestNotificationRepo_CreateForChannels(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	repo := pg.NewNotificationRepo(pool)
	notification := fixtureNotification()

	err := repo.CreateForChannels(t.Context(), notification, []domain.Channel{domain.ChannelLog})

	require.NoError(t, err)

	var (
		eventID, eventType, channel, status string
		payload                             []byte
		attempts                            int
	)
	require.NoError(t, pool.QueryRow(
		t.Context(),
		`SELECT event_id::text, event_type, channel, payload, status, attempts FROM notifications`,
	).Scan(&eventID, &eventType, &channel, &payload, &status, &attempts))
	assert.Equal(t, notification.EventID, eventID)
	assert.Equal(t, notification.EventType, eventType)
	assert.Equal(t, string(domain.ChannelLog), channel)
	assert.JSONEq(t, string(notification.Payload), string(payload))
	assert.Equal(t, "pending", status)
	assert.Zero(t, attempts)
}

func TestNotificationRepo_CreateForChannels_RepeatedPairIsNoop(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	repo := pg.NewNotificationRepo(pool)
	notification := fixtureNotification()
	channels := []domain.Channel{domain.ChannelLog}

	require.NoError(t, repo.CreateForChannels(t.Context(), notification, channels))
	err := repo.CreateForChannels(t.Context(), notification, channels)

	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM notifications`).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestNotificationRepo_CreateForChannels_DuplicateChannelsInOneCall(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	repo := pg.NewNotificationRepo(pool)

	err := repo.CreateForChannels(
		t.Context(),
		fixtureNotification(),
		[]domain.Channel{domain.ChannelLog, domain.ChannelLog},
	)

	require.NoError(t, err)

	var count int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM notifications`).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestNotificationRepo_CreateForChannels_EmptyChannels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		channels []domain.Channel
	}{
		{name: "nil channels", channels: nil},
		{name: "empty channels", channels: []domain.Channel{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pool := pgtest.NewDB(t, migrationsDir)
			repo := pg.NewNotificationRepo(pool)

			err := repo.CreateForChannels(t.Context(), fixtureNotification(), tt.channels)

			require.NoError(t, err)

			var count int
			require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM notifications`).Scan(&count))
			assert.Zero(t, count)
		})
	}
}

func TestNotificationRepo_CreateForChannels_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		notification func() domain.Notification
		channels     []domain.Channel
		want         *apperr.Error
	}{
		{
			name:         "unknown channel violates check",
			notification: fixtureNotification,
			channels:     []domain.Channel{"sms"},
			want:         apperr.InvalidArgument(),
		},
		{
			name:         "valid channel followed by unknown one",
			notification: fixtureNotification,
			channels:     []domain.Channel{domain.ChannelLog, "sms"},
			want:         apperr.InvalidArgument(),
		},
		{
			name: "payload is not valid json",
			notification: func() domain.Notification {
				n := fixtureNotification()
				n.Payload = []byte(`not json`)
				return n
			},
			channels: []domain.Channel{domain.ChannelLog},
			want:     apperr.InvalidArgument(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pool := pgtest.NewDB(t, migrationsDir)
			repo := pg.NewNotificationRepo(pool)

			err := repo.CreateForChannels(t.Context(), tt.notification(), tt.channels)

			require.ErrorIs(t, err, tt.want)
		})
	}
}
