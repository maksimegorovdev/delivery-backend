//go:build integration

package pg_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/repository/pg"
)

func fixtureInboxEvent() domain.InboxEvent {
	return domain.InboxEvent{
		EventID:     fixtureEventID,
		Topic:       "order.events",
		Partition:   3,
		Offset:      42,
		EventType:   "order.created",
		AggregateID: fixtureAggregateID,
	}
}

func TestInboxRepo_Claim(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	repo := pg.NewInboxRepo(pool)
	event := fixtureInboxEvent()

	err := repo.Claim(t.Context(), event)

	require.NoError(t, err)

	var got domain.InboxEvent
	require.NoError(t, pool.QueryRow(
		t.Context(),
		`SELECT event_id::text, topic, partition, msg_offset, event_type, aggregate_id::text
		FROM inbox_events WHERE event_id = $1`,
		event.EventID,
	).Scan(&got.EventID, &got.Topic, &got.Partition, &got.Offset, &got.EventType, &got.AggregateID))
	assert.Equal(t, event, got)

	var receivedAtSet bool
	require.NoError(t, pool.QueryRow(
		t.Context(),
		`SELECT received_at IS NOT NULL FROM inbox_events WHERE event_id = $1`,
		event.EventID,
	).Scan(&receivedAtSet))
	assert.True(t, receivedAtSet)
}

func TestInboxRepo_Claim_RepeatedEventIsNoop(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	repo := pg.NewInboxRepo(pool)
	first := fixtureInboxEvent()
	// Повторная доставка того же события приходит с другого смещения.
	second := first
	second.Partition = 7
	second.Offset = 100

	require.NoError(t, repo.Claim(t.Context(), first))
	err := repo.Claim(t.Context(), second)

	require.NoError(t, err)

	var (
		count     int
		partition int32
		offset    int64
	)
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM inbox_events`).Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, pool.QueryRow(
		t.Context(),
		`SELECT partition, msg_offset FROM inbox_events WHERE event_id = $1`,
		first.EventID,
	).Scan(&partition, &offset))
	assert.Equal(t, first.Partition, partition, "первая запись не должна быть перезаписана")
	assert.Equal(t, first.Offset, offset, "первая запись не должна быть перезаписана")
}

func TestInboxRepo_Claim_DifferentEventsAreStoredSeparately(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	repo := pg.NewInboxRepo(pool)
	first := fixtureInboxEvent()
	second := first
	second.EventID = fixtureOtherEventID
	second.Offset = 43

	require.NoError(t, repo.Claim(t.Context(), first))
	require.NoError(t, repo.Claim(t.Context(), second))

	var count int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM inbox_events`).Scan(&count))
	assert.Equal(t, 2, count)
}
