//go:build integration

//nolint:testpackage // тест использует неэкспортируемые символы пакета
package pgtest

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMigrationsDir = "testdata"

func TestSharedServer_RejectsOtherMigrationsDir(t *testing.T) {
	t.Parallel()

	_, err := sharedServer(testMigrationsDir)
	require.NoError(t, err)

	_, err = sharedServer(t.TempDir())

	require.ErrorContains(t, err, "template database is built from")
}

func TestApplyMigrations(t *testing.T) {
	t.Parallel()

	srv, err := sharedServer(testMigrationsDir)
	require.NoError(t, err)

	t.Run("missing migrations dir", func(t *testing.T) {
		t.Parallel()

		applyErr := applyMigrations(srv.dsn, filepath.Join(t.TempDir(), "missing"))

		require.ErrorContains(t, applyErr, "init migrate for")
	})

	t.Run("invalid migration sql", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		// Клон шаблона уже на версии 1, поэтому битая миграция получает версию 2.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "000002_broken.up.sql"), []byte("NOT SQL;"), 0o600))

		const name = "t_apply_broken"
		require.NoError(t, createDatabase(t.Context(), srv, name))
		t.Cleanup(func() {
			assert.NoError(t, dropDatabase(context.WithoutCancel(t.Context()), srv, name))
		})

		u, parseErr := url.Parse(srv.dsn)
		require.NoError(t, parseErr)
		u.Path = "/" + name

		applyErr := applyMigrations(u.String(), dir)

		require.ErrorContains(t, applyErr, "apply migrations from")
	})
}

func TestExecAdmin(t *testing.T) {
	t.Parallel()

	srv, err := sharedServer(testMigrationsDir)
	require.NoError(t, err)

	t.Run("invalid dsn", func(t *testing.T) {
		t.Parallel()

		execErr := execAdmin(t.Context(), server{dsn: "://bad"}, "SELECT 1")

		require.ErrorContains(t, execErr, "parse dsn")
	})

	t.Run("unreachable server", func(t *testing.T) {
		t.Parallel()

		unreachable := server{dsn: "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1"}

		execErr := execAdmin(t.Context(), unreachable, "SELECT 1")

		require.ErrorContains(t, execErr, "connect to postgres database")
	})

	t.Run("invalid query", func(t *testing.T) {
		t.Parallel()

		execErr := execAdmin(t.Context(), srv, "NOT SQL")

		require.ErrorContains(t, execErr, "exec")
	})
}
