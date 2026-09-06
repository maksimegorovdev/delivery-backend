package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/pgerr"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type userRow struct {
	ID        string     `db:"id"`
	Email     string     `db:"email"`
	FirstName string     `db:"first_name"`
	LastName  string     `db:"last_name"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt time.Time  `db:"updated_at"`
	DeletedAt *time.Time `db:"deleted_at"`
}

func (r userRow) toDomain() domain.User {
	return domain.User{
		ID:        r.ID,
		Email:     r.Email,
		FirstName: r.FirstName,
		LastName:  r.LastName,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

type UserRepository struct {
	pool *pgxpool.Pool
}

func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

func (r *UserRepository) GetByID(ctx context.Context, id string) (domain.User, error) {
	rows, err := r.pool.Query(
		ctx,
		`SELECT id, email, first_name, last_name, created_at, updated_at, deleted_at
		FROM users WHERE id = $1 AND deleted_at IS NULL`,
		id,
	)
	if err != nil {
		return domain.User{}, pgerr.Map(err)
	}

	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[userRow])
	if err != nil {
		return domain.User{}, pgerr.Map(err)
	}
	return row.toDomain(), nil
}
