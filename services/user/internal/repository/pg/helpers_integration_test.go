//go:build integration

package pg_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

const migrationsDir = "../../../migrations/pg"

// Фиксированные идентификаторы собственных фикстур: на seed-миграции тесты не опираются.
const (
	fixtureUserID         = "0190b000-0000-7000-8000-000000000001"
	fixtureOtherUserID    = "0190b000-0000-7000-8000-000000000002"
	fixtureAddressID      = "0190b000-0000-7000-8000-000000000011"
	fixtureOtherAddressID = "0190b000-0000-7000-8000-000000000012"

	// missingID — корректный UUID, которого нет ни в одной таблице.
	missingID = "0190b000-0000-7000-8000-0000000000ff"
)

// fixtureUser возвращает пользователя с фиксированными идентификатором и временем.
// created_at и updated_at различаются, чтобы перепутанный маппинг колонок не прошёл незамеченным.
func fixtureUser() domain.User {
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	return domain.User{
		ID:        fixtureUserID,
		Email:     "ivan.petrov@fixture.test",
		FirstName: "Ivan",
		LastName:  "Petrov",
		CreatedAt: createdAt,
		UpdatedAt: createdAt.Add(time.Hour),
	}
}

// fixtureOtherUser возвращает второго пользователя: проверяет, что выборка идёт именно по id.
func fixtureOtherUser() domain.User {
	createdAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

	return domain.User{
		ID:        fixtureOtherUserID,
		Email:     "anna.sidorova@fixture.test",
		FirstName: "Anna",
		LastName:  "Sidorova",
		CreatedAt: createdAt,
		UpdatedAt: createdAt.Add(time.Hour),
	}
}

// fixtureAddress возвращает адрес пользователя fixtureUserID.
func fixtureAddress() domain.Address {
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	return domain.Address{
		ID:        fixtureAddressID,
		UserID:    fixtureUserID,
		Address:   "Lenina st, 1",
		CreatedAt: createdAt,
		UpdatedAt: createdAt.Add(time.Hour),
	}
}

// fixtureOtherAddress возвращает второй адрес того же пользователя.
func fixtureOtherAddress() domain.Address {
	createdAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

	return domain.Address{
		ID:        fixtureOtherAddressID,
		UserID:    fixtureUserID,
		Address:   "Pushkina st, 2",
		CreatedAt: createdAt,
		UpdatedAt: createdAt.Add(time.Hour),
	}
}

func insertUser(t *testing.T, pool *pgxpool.Pool, u domain.User) {
	t.Helper()

	_, err := pool.Exec(
		t.Context(),
		`INSERT INTO users (id, email, first_name, last_name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		u.ID,
		u.Email,
		u.FirstName,
		u.LastName,
		u.CreatedAt,
		u.UpdatedAt,
	)
	require.NoError(t, err)
}

func insertAddress(t *testing.T, pool *pgxpool.Pool, a domain.Address) {
	t.Helper()

	_, err := pool.Exec(
		t.Context(),
		`INSERT INTO addresses (id, user_id, address, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)`,
		a.ID,
		a.UserID,
		a.Address,
		a.CreatedAt,
		a.UpdatedAt,
	)
	require.NoError(t, err)
}

// userInUTC приводит время к UTC: pgx отдаёт timestamptz в локальной зоне, а фикстуры заданы в UTC.
func userInUTC(u domain.User) domain.User {
	u.CreatedAt = u.CreatedAt.UTC()
	u.UpdatedAt = u.UpdatedAt.UTC()

	return u
}

// addressInUTC — то же для адреса, см. userInUTC.
func addressInUTC(a domain.Address) domain.Address {
	a.CreatedAt = a.CreatedAt.UTC()
	a.UpdatedAt = a.UpdatedAt.UTC()

	return a
}
