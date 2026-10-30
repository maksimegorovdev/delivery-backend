//go:build integration

package pg_test

import (
	"testing"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"
)

func TestMigrations(t *testing.T) {
	t.Parallel()

	pgtest.CheckMigrations(t, migrationsDir)
}
