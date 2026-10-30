//go:build integration

package pg_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/repository/pg"
)

func TestUserRepo_GetByID(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	insertUser(t, pool, fixtureUser())
	insertUser(t, pool, fixtureOtherUser())
	repo := pg.NewUserRepo(pool)

	got, err := repo.GetByID(t.Context(), fixtureUserID)

	require.NoError(t, err)
	assert.Equal(t, fixtureUser(), userInUTC(got))
}

func TestUserRepo_GetByID_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		want *apperr.Error
	}{
		{name: "unknown id", id: missingID, want: apperr.NotFound()},
		{name: "not a uuid", id: "not-a-uuid", want: apperr.InvalidArgument()},
		{name: "empty id", id: "", want: apperr.InvalidArgument()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pool := pgtest.NewDB(t, migrationsDir)
			insertUser(t, pool, fixtureUser())
			repo := pg.NewUserRepo(pool)

			got, err := repo.GetByID(t.Context(), tt.id)

			require.ErrorIs(t, err, tt.want)
			assert.Equal(t, domain.User{}, got)
		})
	}
}
