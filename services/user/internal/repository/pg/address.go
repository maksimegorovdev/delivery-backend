package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/pgerr"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type addressRow struct {
	ID        string    `db:"id"`
	UserID    string    `db:"user_id"`
	Address   string    `db:"address"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (r addressRow) toDomain() domain.Address {
	return domain.Address{
		ID:        r.ID,
		UserID:    r.UserID,
		Address:   r.Address,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

type AddressRepo struct {
	pool *pgxpool.Pool
}

func NewAddressRepo(pool *pgxpool.Pool) *AddressRepo {
	return &AddressRepo{pool: pool}
}

func (r *AddressRepo) GetByID(ctx context.Context, id string) (domain.Address, error) {
	rows, err := r.pool.Query(
		ctx,
		`SELECT id, user_id, address, created_at, updated_at
		FROM addresses WHERE id = $1`,
		id,
	)
	if err != nil {
		return domain.Address{}, pgerr.Map(err)
	}

	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[addressRow])
	if err != nil {
		return domain.Address{}, pgerr.Map(err)
	}
	return row.toDomain(), nil
}
