//go:build integration

package pgtest_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"
)

const migrationsDir = "testdata"

func TestNewDB_AppliesMigrations(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)

	var table *string
	err := pool.QueryRow(t.Context(), `SELECT to_regclass('public.items')::text`).Scan(&table)

	require.NoError(t, err)
	require.NotNil(t, table, "таблица items из миграции должна существовать")
	assert.Equal(t, "items", *table)
}

func TestNewDB_IsolatesParallelDatabases(t *testing.T) {
	t.Parallel()

	const dbCount = 10

	databases := make(chan string, dbCount)

	// Cleanup родителя выполняется после завершения всех параллельных подтестов.
	t.Cleanup(func() {
		close(databases)

		unique := make(map[string]struct{}, dbCount)
		for name := range databases {
			unique[name] = struct{}{}
		}
		assert.Len(t, unique, dbCount, "каждый NewDB должен получать отдельную БД")
	})

	for i := range dbCount {
		t.Run(fmt.Sprintf("db-%d", i), func(t *testing.T) {
			t.Parallel()

			pool := pgtest.NewDB(t, migrationsDir)

			_, err := pool.Exec(t.Context(), `INSERT INTO items (name) VALUES ($1)`, fmt.Sprintf("item-%d", i))
			require.NoError(t, err)

			var count int
			require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM items`).Scan(&count))
			assert.Equal(t, 1, count, "запись из другой БД не должна быть видна")

			var name string
			require.NoError(t, pool.QueryRow(t.Context(), `SELECT current_database()`).Scan(&name))
			databases <- name
		})
	}
}
