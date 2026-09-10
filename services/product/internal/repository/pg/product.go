package pg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/pgerr"
	"github.com/maksimegorovdev/delivery-backend/services/product/internal/domain"
)

type productRow struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	Price     int64     `db:"price"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (r productRow) toDomain() domain.Product {
	return domain.Product{
		ID:        r.ID,
		Name:      r.Name,
		Price:     r.Price,
		CreatedAt: r.CreatedAt,
		UpdatedAt: r.UpdatedAt,
	}
}

type ProductRepo struct {
	pool *pgxpool.Pool
}

func NewProductRepo(pool *pgxpool.Pool) *ProductRepo {
	return &ProductRepo{pool: pool}
}

func (r *ProductRepo) GetByIDs(ctx context.Context, ids []string) ([]domain.Product, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := r.pool.Query(
		ctx,
		`SELECT id, name, price, created_at, updated_at
		FROM products WHERE id = ANY($1::uuid[])`,
		ids,
	)
	if err != nil {
		return nil, pgerr.Map(err)
	}

	list, err := pgx.CollectRows(rows, pgx.RowToStructByName[productRow])
	if err != nil {
		return nil, pgerr.Map(err)
	}

	out := make([]domain.Product, len(list))
	for i, row := range list {
		out[i] = row.toDomain()
	}
	return out, nil
}
