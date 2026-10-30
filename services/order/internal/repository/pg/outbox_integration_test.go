//go:build integration

package pg_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/repository/pg"
)

func TestOutboxRepo_Save(t *testing.T) {
	t.Parallel()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)
	state, err := trace.ParseTraceState("vendor=value")
	require.NoError(t, err)

	tests := []struct {
		name            string
		ctx             func(t *testing.T) context.Context
		wantTraceparent *string
		wantTracestate  *string
	}{
		{
			name: "active span",
			ctx: func(t *testing.T) context.Context {
				t.Helper()

				return trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{
					TraceID:    traceID,
					SpanID:     spanID,
					TraceFlags: trace.FlagsSampled,
					TraceState: state,
					Remote:     true,
				}))
			},
			wantTraceparent: new("00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"),
			wantTracestate:  new("vendor=value"),
		},
		{
			name: "no span",
			ctx: func(t *testing.T) context.Context {
				t.Helper()

				return t.Context()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pool := pgtest.NewDB(t, migrationsDir)
			repo := pg.NewOutboxRepo(pool)
			event := fixtureEvent()

			require.NoError(t, repo.Save(tt.ctx(t), event))

			var (
				id, aggregateType, aggregateID, eventType string
				payload                                   []byte
				createdAt                                 time.Time
				traceparent, tracestate                   *string
			)
			require.NoError(t, pool.QueryRow(
				t.Context(),
				`SELECT id::text, aggregate_type, aggregate_id::text, type, payload, created_at, traceparent, tracestate
				FROM outbox_events WHERE id = $1`,
				event.ID,
			).Scan(
				&id,
				&aggregateType,
				&aggregateID,
				&eventType,
				&payload,
				&createdAt,
				&traceparent,
				&tracestate,
			))

			assert.Equal(t, event.ID, id)
			assert.Equal(t, event.AggregateType, aggregateType)
			assert.Equal(t, event.AggregateID, aggregateID)
			assert.Equal(t, event.Type, eventType)
			assert.JSONEq(t, string(event.Payload), string(payload))
			assert.True(
				t,
				event.CreatedAt.Equal(createdAt),
				"created_at: want %s, got %s",
				event.CreatedAt,
				createdAt,
			)
			assert.Equal(t, tt.wantTraceparent, traceparent)
			assert.Equal(t, tt.wantTracestate, tracestate)
		})
	}
}

func TestOutboxRepo_Save_DuplicateID(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	repo := pg.NewOutboxRepo(pool)

	require.NoError(t, repo.Save(t.Context(), fixtureEvent()))

	err := repo.Save(t.Context(), fixtureEvent())

	require.ErrorIs(t, err, apperr.AlreadyExists())
}
