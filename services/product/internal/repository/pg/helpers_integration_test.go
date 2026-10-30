//go:build integration

package pg_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/services/product/internal/domain"
)

const migrationsDir = "../../../migrations/pg"

// Фиксированные идентификаторы собственных фикстур: на seed-миграции тесты не опираются.
const (
	fixtureProductID      = "0190c000-0000-7000-8000-000000000001"
	fixtureOtherProductID = "0190c000-0000-7000-8000-000000000002"

	// missingID — корректный UUID, которого нет в таблице.
	missingID = "0190c000-0000-7000-8000-0000000000ff"
)

// fixtureProduct возвращает товар с фиксированными идентификатором и временем.
// created_at и updated_at различаются, чтобы перепутанный маппинг колонок не прошёл незамеченным.
func fixtureProduct() domain.Product {
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	return domain.Product{
		ID:        fixtureProductID,
		Name:      "Fixture Pizza",
		Price:     49900,
		CreatedAt: createdAt,
		UpdatedAt: createdAt.Add(time.Hour),
	}
}

// fixtureOtherProduct возвращает второй товар: проверяет, что выборка идёт именно по id.
func fixtureOtherProduct() domain.Product {
	createdAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

	return domain.Product{
		ID:        fixtureOtherProductID,
		Name:      "Fixture Burger",
		Price:     29900,
		CreatedAt: createdAt,
		UpdatedAt: createdAt.Add(time.Hour),
	}
}

func insertProduct(t *testing.T, pool *pgxpool.Pool, p domain.Product) {
	t.Helper()

	_, err := pool.Exec(
		t.Context(),
		`INSERT INTO products (id, name, price, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)`,
		p.ID,
		p.Name,
		p.Price,
		p.CreatedAt,
		p.UpdatedAt,
	)
	require.NoError(t, err)
}

// productInUTC приводит время к UTC: pgx отдаёт timestamptz в локальной зоне, а фикстуры заданы в UTC.
func productInUTC(p domain.Product) domain.Product {
	p.CreatedAt = p.CreatedAt.UTC()
	p.UpdatedAt = p.UpdatedAt.UTC()

	return p
}

func productsInUTC(list []domain.Product) []domain.Product {
	out := make([]domain.Product, len(list))
	for i, p := range list {
		out[i] = productInUTC(p)
	}

	return out
}
