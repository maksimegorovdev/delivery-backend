package pg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/pgerr"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

type OrderRepo struct {
	pool *pgxpool.Pool
}

func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo {
	return &OrderRepo{pool: pool}
}

func (r *OrderRepo) Create(ctx context.Context, order domain.Order) error {
	db := postgres.DB(ctx, r.pool)

	if err := r.insertOrder(ctx, db, order); err != nil {
		return err
	}

	return r.insertItems(ctx, db, order.Items)
}

func (r *OrderRepo) insertOrder(
	ctx context.Context,
	db postgres.Executor,
	order domain.Order,
) error {
	if _, err := db.Exec(
		ctx,
		`INSERT INTO orders (id, user_id, status, total_amount, delivery_address, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		order.ID, order.UserID, order.Status, order.TotalAmount, order.DeliveryAddress, order.CreatedAt, order.UpdatedAt,
	); err != nil {
		return pgerr.Map(err)
	}
	return nil
}

func (r *OrderRepo) insertItems(
	ctx context.Context,
	db postgres.Executor,
	items []domain.OrderItem,
) error {
	columns := []string{
		"id",
		"order_id",
		"product_id",
		"product_name",
		"quantity",
		"unit_price",
	}

	_, err := db.CopyFrom(
		ctx,
		pgx.Identifier{"order_items"},
		columns,
		pgx.CopyFromSlice(len(items), func(i int) ([]any, error) {
			it := items[i]
			return []any{
				it.ID,
				it.OrderID,
				it.ProductID,
				it.ProductName,
				it.Quantity,
				it.UnitPrice,
			}, nil
		}),
	)
	if err != nil {
		return pgerr.Map(err)
	}
	return nil
}
