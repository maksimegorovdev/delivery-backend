//go:build integration

package pg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/repository/pg"
)

// rowCounts возвращает число строк в таблицах заказа и outbox.
type rowCounts struct {
	orders, items, events int
}

func countRows(t *testing.T, pool *pgxpool.Pool) rowCounts {
	t.Helper()

	var got rowCounts
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM orders`).Scan(&got.orders))
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM order_items`).Scan(&got.items))
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events`).Scan(&got.events))

	return got
}

func TestTxManager_OrderWithOutbox_Commit(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	txm := postgres.NewTxManager(pool)
	orders := pg.NewOrderRepo(pool)
	outbox := pg.NewOutboxRepo(pool)

	err := txm.WithinTx(t.Context(), func(ctx context.Context) error {
		if err := orders.Create(ctx, fixtureOrder()); err != nil {
			return err
		}

		return outbox.Save(ctx, fixtureEvent())
	})

	require.NoError(t, err)
	assert.Equal(t, rowCounts{orders: 1, items: 1, events: 1}, countRows(t, pool))
}

func TestTxManager_OrderWithOutbox_Rollback(t *testing.T) {
	t.Parallel()

	errAfterSave := errors.New("fail after save")

	tests := []struct {
		name      string
		prepare   func(t *testing.T, outbox *pg.OutboxRepo)
		afterSave func() error
		wantErr   error
		want      rowCounts
	}{
		{
			name: "outbox save fails on duplicate event id",
			prepare: func(t *testing.T, outbox *pg.OutboxRepo) {
				t.Helper()
				// Событие с тем же id уже лежит вне транзакции: единственная строка в outbox — оно.
				require.NoError(t, outbox.Save(t.Context(), fixtureEvent()))
			},
			afterSave: func() error { return nil },
			wantErr:   apperr.AlreadyExists(),
			want:      rowCounts{events: 1},
		},
		{
			name:      "callback fails after outbox save",
			afterSave: func() error { return errAfterSave },
			wantErr:   errAfterSave,
			want:      rowCounts{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pool := pgtest.NewDB(t, migrationsDir)
			txm := postgres.NewTxManager(pool)
			orders := pg.NewOrderRepo(pool)
			outbox := pg.NewOutboxRepo(pool)
			if tt.prepare != nil {
				tt.prepare(t, outbox)
			}

			err := txm.WithinTx(t.Context(), func(ctx context.Context) error {
				if err := orders.Create(ctx, fixtureOrder()); err != nil {
					return err
				}
				if err := outbox.Save(ctx, fixtureEvent()); err != nil {
					return err
				}

				return tt.afterSave()
			})

			require.ErrorIs(t, err, tt.wantErr)
			assert.Equal(t, tt.want, countRows(t, pool))
		})
	}
}
