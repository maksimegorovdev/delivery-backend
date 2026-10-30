//go:build integration

package pg_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"

	"github.com/maksimegorovdev/delivery-backend/services/product/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/product/internal/repository/pg"
)

func TestProductRepo_GetByIDs(t *testing.T) {
	t.Parallel()

	t.Run("all found", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)
		insertProduct(t, pool, fixtureProduct())
		insertProduct(t, pool, fixtureOtherProduct())
		repo := pg.NewProductRepo(pool)

		got, err := repo.GetByIDs(t.Context(), []string{fixtureProductID, fixtureOtherProductID})

		require.NoError(t, err)
		// Порядок выборки без ORDER BY не гарантирован.
		assert.ElementsMatch(t, []domain.Product{fixtureProduct(), fixtureOtherProduct()}, productsInUTC(got))
	})

	t.Run("only requested ids are returned", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)
		insertProduct(t, pool, fixtureProduct())
		insertProduct(t, pool, fixtureOtherProduct())
		repo := pg.NewProductRepo(pool)

		got, err := repo.GetByIDs(t.Context(), []string{fixtureProductID})

		require.NoError(t, err)
		assert.Equal(t, []domain.Product{fixtureProduct()}, productsInUTC(got))
	})

	t.Run("missing ids are skipped without error", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)
		insertProduct(t, pool, fixtureProduct())
		repo := pg.NewProductRepo(pool)

		got, err := repo.GetByIDs(t.Context(), []string{fixtureProductID, missingID})

		require.NoError(t, err)
		assert.Equal(t, []domain.Product{fixtureProduct()}, productsInUTC(got))
	})

	t.Run("no ids found returns empty list", func(t *testing.T) {
		t.Parallel()

		pool := pgtest.NewDB(t, migrationsDir)
		insertProduct(t, pool, fixtureProduct())
		repo := pg.NewProductRepo(pool)

		got, err := repo.GetByIDs(t.Context(), []string{missingID})

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("empty list returns nothing without query", func(t *testing.T) {
		t.Parallel()

		// Репозиторий на закрытом пуле: любой запрос к БД вернул бы ошибку.
		pool := pgtest.NewDB(t, migrationsDir)
		pool.Close()
		repo := pg.NewProductRepo(pool)

		got, err := repo.GetByIDs(t.Context(), nil)

		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestProductRepo_GetByIDs_InvalidID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ids  []string
	}{
		{name: "not a uuid", ids: []string{"not-a-uuid"}},
		{name: "empty id", ids: []string{""}},
		{name: "valid and invalid", ids: []string{fixtureProductID, "not-a-uuid"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pool := pgtest.NewDB(t, migrationsDir)
			insertProduct(t, pool, fixtureProduct())
			repo := pg.NewProductRepo(pool)

			got, err := repo.GetByIDs(t.Context(), tt.ids)

			require.ErrorIs(t, err, apperr.InvalidArgument())
			assert.Nil(t, got)
		})
	}
}
