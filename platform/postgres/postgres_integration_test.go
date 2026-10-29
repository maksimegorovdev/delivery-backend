//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"
)

const migrationsDir = "testdata"

// dsnOf собирает DSN к той же БД, что и у пула из pgtest.NewDB.
func dsnOf(pool *pgxpool.Pool) string {
	cfg := pool.Config().ConnConfig

	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     net.JoinHostPort(cfg.Host, strconv.Itoa(int(cfg.Port))),
		Path:     cfg.Database,
		RawQuery: "sslmode=disable",
	}

	return u.String()
}

func countItems(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()

	var n int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM items`).Scan(&n))

	return n
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("connects with default max conns", func(t *testing.T) {
		t.Parallel()

		dsn := dsnOf(pgtest.NewDB(t, migrationsDir))

		pool, err := postgres.New(t.Context(), dsn)

		require.NoError(t, err)
		t.Cleanup(pool.Close)
		assert.EqualValues(t, 10, pool.Config().MaxConns)
		require.NoError(t, pool.Ping(t.Context()))
	})

	t.Run("applies WithMaxConns", func(t *testing.T) {
		t.Parallel()

		dsn := dsnOf(pgtest.NewDB(t, migrationsDir))

		pool, err := postgres.New(t.Context(), dsn, postgres.WithMaxConns(3))

		require.NoError(t, err)
		t.Cleanup(pool.Close)
		assert.EqualValues(t, 3, pool.Config().MaxConns)
	})

	t.Run("invalid dsn", func(t *testing.T) {
		t.Parallel()

		pool, err := postgres.New(t.Context(), "://bad")

		require.ErrorContains(t, err, "postgres: parse dsn")
		assert.Nil(t, pool)
	})

	t.Run("unreachable server fails on ping", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		pool, err := postgres.New(ctx, "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")

		require.ErrorContains(t, err, "postgres: ping")
		assert.Nil(t, pool)
	})
}

func TestDB(t *testing.T) {
	t.Parallel()

	t.Run("returns pool outside of transaction", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)

		assert.Same(t, pool, postgres.DB(t.Context(), pool))
	})

	t.Run("returns transaction inside WithinTx", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)

		err := postgres.NewTxManager(pool).WithinTx(t.Context(), func(ctx context.Context) error {
			assert.NotSame(t, pool, postgres.DB(ctx, pool))
			return nil
		})

		require.NoError(t, err)
	})
}

func TestTxManager_WithinTx(t *testing.T) {
	t.Parallel()

	insert := func(ctx context.Context, pool *pgxpool.Pool, name string) error {
		_, err := postgres.DB(ctx, pool).Exec(ctx, `INSERT INTO items (name) VALUES ($1)`, name)
		return err
	}

	t.Run("commits on success", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)

		err := postgres.NewTxManager(pool).WithinTx(t.Context(), func(ctx context.Context) error {
			return insert(ctx, pool, "committed")
		})

		require.NoError(t, err)
		assert.Equal(t, 1, countItems(t, pool))
	})

	t.Run("rolls back and returns callback error", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)
		errBoom := errors.New("boom")

		err := postgres.NewTxManager(pool).WithinTx(t.Context(), func(ctx context.Context) error {
			require.NoError(t, insert(ctx, pool, "rolled back"))
			return errBoom
		})

		require.ErrorIs(t, err, errBoom)
		assert.Zero(t, countItems(t, pool))
	})

	t.Run("nested call joins the outer transaction", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)
		manager := postgres.NewTxManager(pool)
		errOuter := errors.New("outer failed")

		err := manager.WithinTx(t.Context(), func(ctx context.Context) error {
			innerErr := manager.WithinTx(ctx, func(ctx context.Context) error {
				return insert(ctx, pool, "inner")
			})
			require.NoError(t, innerErr)

			return errOuter
		})

		require.ErrorIs(t, err, errOuter)
		assert.Zero(t, countItems(t, pool), "внутренний вызов не должен коммитить отдельно")
	})

	t.Run("begin on closed pool is mapped to internal", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)
		pool.Close()

		err := postgres.NewTxManager(pool).WithinTx(t.Context(), func(context.Context) error {
			t.Error("callback must not run when begin fails")
			return nil
		})

		appErr, ok := apperr.As(err)
		require.True(t, ok)
		assert.Equal(t, apperr.CodeInternal, appErr.Code)
	})

	t.Run("commit failure is mapped", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)
		_, err := pool.Exec(t.Context(), `CREATE TABLE deferred (id INT UNIQUE DEFERRABLE INITIALLY DEFERRED)`)
		require.NoError(t, err)

		err = postgres.NewTxManager(pool).WithinTx(t.Context(), func(ctx context.Context) error {
			// Нарушение уникальности проверяется только на COMMIT.
			_, execErr := postgres.DB(ctx, pool).Exec(ctx, `INSERT INTO deferred (id) VALUES (1), (1)`)
			return execErr
		})

		appErr, ok := apperr.As(err)
		require.True(t, ok)
		assert.Equal(t, apperr.CodeAlreadyExists, appErr.Code)
	})
}
