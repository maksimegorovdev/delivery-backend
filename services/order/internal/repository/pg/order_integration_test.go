//go:build integration

package pg_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/repository/pg"
)

const migrationsDir = "../../../migrations/pg"

func newOrder(t *testing.T) domain.Order {
	t.Helper()

	order, err := domain.NewOrder(
		"0190a000-0000-7000-8000-000000000001",
		"Lenina st, 1",
		[]domain.OrderItem{
			{ProductID: "0190a000-0000-7000-8000-0000000000a1", ProductName: "Bread", Quantity: 2, UnitPrice: 5000},
			{ProductID: "0190a000-0000-7000-8000-0000000000a2", ProductName: "Milk", Quantity: 1, UnitPrice: 9000},
		},
	)
	require.NoError(t, err)

	return order
}

func TestOrderRepo_Create(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	repo := pg.NewOrderRepo(pool)
	order := newOrder(t)

	err := repo.Create(t.Context(), order)

	require.NoError(t, err)

	var (
		userID, status, address string
		total                   int64
	)
	require.NoError(t, pool.QueryRow(
		t.Context(),
		`SELECT user_id::text, status, total_amount, delivery_address FROM orders WHERE id = $1`,
		order.ID,
	).Scan(&userID, &status, &total, &address))
	assert.Equal(t, order.UserID, userID)
	assert.Equal(t, string(domain.OrderStatusCreated), status)
	assert.Equal(t, int64(19000), total)
	assert.Equal(t, order.DeliveryAddress, address)

	rows, err := pool.Query(
		t.Context(),
		`SELECT id::text, product_id::text, product_name, quantity, unit_price
		FROM order_items WHERE order_id = $1 ORDER BY product_name`,
		order.ID,
	)
	require.NoError(t, err)
	defer rows.Close()

	var got []domain.OrderItem
	for rows.Next() {
		var it domain.OrderItem
		require.NoError(t, rows.Scan(&it.ID, &it.ProductID, &it.ProductName, &it.Quantity, &it.UnitPrice))
		it.OrderID = order.ID
		got = append(got, it)
	}
	require.NoError(t, rows.Err())

	// Bread < Milk по алфавиту, порядок Items в заказе такой же.
	assert.Equal(t, order.Items, got)
}

func TestOrderRepo_Create_IsolatedFromParallelTest(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	repo := pg.NewOrderRepo(pool)

	// Соседний параллельный тест пишет в свою БД, здесь записей быть не должно.
	var ordersCount, itemsCount int
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM orders`).Scan(&ordersCount))
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM order_items`).Scan(&itemsCount))
	assert.Zero(t, ordersCount)
	assert.Zero(t, itemsCount)

	require.NoError(t, repo.Create(t.Context(), newOrder(t)))

	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM orders`).Scan(&ordersCount))
	assert.Equal(t, 1, ordersCount)
}
