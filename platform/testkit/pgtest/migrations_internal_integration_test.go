//go:build integration

//nolint:testpackage // тест использует неэкспортируемые символы пакета
package pgtest

import (
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestCheckMigrations_Internal(t *testing.T) {
	t.Parallel()

	srv, err := sharedServer(testMigrationsDir)
	require.NoError(t, err)

	t.Run("valid migrations pass the full cycle", func(t *testing.T) {
		t.Parallel()

		checkErr := checkMigrations(t.Context(), srv, "t_check_valid", srv.migrationsDir)

		require.NoError(t, checkErr)
	})

	t.Run("incomplete down is detected", func(t *testing.T) {
		t.Parallel()

		leakyDir, absErr := filepath.Abs(filepath.Join(testMigrationsDir, "leaky_down"))
		require.NoError(t, absErr)

		checkErr := checkMigrations(t.Context(), srv, "t_check_leaky", leakyDir)

		require.ErrorContains(t, checkErr, "schema public is not empty")
		require.ErrorContains(t, checkErr, "audit_log")
	})

	t.Run("missing migrations dir returns error", func(t *testing.T) {
		t.Parallel()

		checkErr := checkMigrations(t.Context(), srv, "t_check_missing", filepath.Join(t.TempDir(), "missing"))

		require.ErrorContains(t, checkErr, "init migrate for")
	})

	t.Run("database is dropped after the check", func(t *testing.T) {
		t.Parallel()

		const name = "t_check_dropped"
		leakyDir, absErr := filepath.Abs(filepath.Join(testMigrationsDir, "leaky_down"))
		require.NoError(t, absErr)

		// Даже при ошибке проверки БД не должна остаться.
		require.Error(t, checkMigrations(t.Context(), srv, name, leakyDir))

		cfg, parseErr := pgx.ParseConfig(srv.dsn)
		require.NoError(t, parseErr)
		cfg.Database = adminDB
		conn, connErr := pgx.ConnectConfig(t.Context(), cfg)
		require.NoError(t, connErr)
		t.Cleanup(func() { _ = conn.Close(t.Context()) })

		var exists bool
		require.NoError(t, conn.QueryRow(
			t.Context(),
			"SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)",
			name,
		).Scan(&exists))
		require.False(t, exists)
	})
}
