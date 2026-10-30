//go:build integration

package pgtest_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"
)

func TestCheckMigrations(t *testing.T) {
	t.Parallel()

	pgtest.CheckMigrations(t, migrationsDir)

	// Шаблон не тронут: NewDB в том же бинаре по-прежнему отдаёт БД с миграциями.
	pool := pgtest.NewDB(t, migrationsDir)

	var exists bool
	require.NoError(t, pool.QueryRow(
		t.Context(),
		"SELECT to_regclass('public.items') IS NOT NULL",
	).Scan(&exists))
	require.True(t, exists)
}
