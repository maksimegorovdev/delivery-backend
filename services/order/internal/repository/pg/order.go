package pg

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/pgerr"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

type OrderRepo struct {
	pool *pgxpool.Pool
}

func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo {
	return &OrderRepo{pool: pool}
}

func (r *OrderRepo) Create(ctx context.Context, order domain.Order) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return pgerr.Map(err)
	}
	defer tx.Rollback(ctx)

	if err := r.insertOrder(ctx, tx, order); err != nil {
		return err
	}

	if err := r.insertItems(ctx, tx, order.Items); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return pgerr.Map(err)
	}
	return nil
}

func (r *OrderRepo) insertOrder(
	ctx context.Context,
	tx pgx.Tx,
	order domain.Order,
) error {
	if _, err := tx.Exec(
		ctx,
		`INSERT INTO orders (id, user_id, status, total_amount, delivery_address, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		order.ID, order.UserID, string(order.Status), order.TotalAmount, order.DeliveryAddress, order.CreatedAt, order.UpdatedAt,
	); err != nil {
		return pgerr.Map(err)
	}
	return nil
}

func (r *OrderRepo) insertItems(
	ctx context.Context,
	tx pgx.Tx,
	items []domain.OrderItem,
) error {
	columns := []string{
		"id",
		"order_id",
		"product_id",
		"product_name",
		"quantity",
		"unit_price",
		"created_at",
		"updated_at",
	}

	_, err := tx.CopyFrom(
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
				it.CreatedAt,
				it.UpdatedAt,
			}, nil
		}),
	)
	if err != nil {
		return pgerr.Map(err)
	}
	return nil
}
