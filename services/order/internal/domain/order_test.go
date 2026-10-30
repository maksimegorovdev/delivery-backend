package domain_test

import (
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

func TestOrderItem_Subtotal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item domain.OrderItem
		want int64
	}{
		{"single unit", domain.OrderItem{Quantity: 1, UnitPrice: 250}, 250},
		{"multiple units", domain.OrderItem{Quantity: 3, UnitPrice: 250}, 750},
		{"zero quantity", domain.OrderItem{Quantity: 0, UnitPrice: 250}, 0},
		{"zero price", domain.OrderItem{Quantity: 4, UnitPrice: 0}, 0},
		{
			"no int32 overflow",
			domain.OrderItem{Quantity: 1 << 30, UnitPrice: 1 << 20},
			int64(1<<30) * int64(1<<20),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.item.Subtotal())
		})
	}
}

func TestNewOrder_NoItems(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		items []domain.OrderItem
	}{
		{"nil items", nil},
		{"empty items", []domain.OrderItem{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.NewOrder("user-1", "addr", tt.items)

			require.ErrorIs(t, err, domain.ErrOrderNoItems)
			assert.Equal(t, domain.Order{}, got)
		})
	}
}

func TestNewOrder_TotalAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		items []domain.OrderItem
		want  int64
	}{
		{
			"one item",
			[]domain.OrderItem{{Quantity: 2, UnitPrice: 100}},
			200,
		},
		{
			"several items",
			[]domain.OrderItem{
				{Quantity: 2, UnitPrice: 100},
				{Quantity: 1, UnitPrice: 550},
				{Quantity: 3, UnitPrice: 10},
			},
			780,
		},
		{
			"free item",
			[]domain.OrderItem{{Quantity: 5, UnitPrice: 0}},
			0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.NewOrder("user-1", "addr", tt.items)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got.TotalAmount)
		})
	}
}

func TestNewOrder_Fields(t *testing.T) {
	t.Parallel()

	items := []domain.OrderItem{
		{ProductID: "p-1", ProductName: "Milk", Quantity: 2, UnitPrice: 100},
		{ProductID: "p-2", ProductName: "Bread", Quantity: 1, UnitPrice: 50},
	}

	before := time.Now()
	got, err := domain.NewOrder("user-1", "Lenina 1", items)
	after := time.Now()

	require.NoError(t, err)

	_, err = uuid.Parse(got.ID)
	require.NoError(t, err, "order ID must be a UUID")

	assert.Equal(t, "user-1", got.UserID)
	assert.Equal(t, "Lenina 1", got.DeliveryAddress)
	assert.Equal(t, domain.OrderStatusCreated, got.Status)

	assert.Same(t, time.UTC, got.CreatedAt.Location())
	assert.Same(t, time.UTC, got.UpdatedAt.Location())
	assert.Equal(t, got.CreatedAt, got.UpdatedAt)
	assert.False(t, got.CreatedAt.Before(before.Truncate(0)), "CreatedAt before call")
	assert.False(t, got.CreatedAt.After(after), "CreatedAt after call")

	require.Len(t, got.Items, len(items))
	seen := map[string]struct{}{got.ID: {}}
	for i, item := range got.Items {
		_, parseErr := uuid.Parse(item.ID)
		require.NoError(t, parseErr, "item %d ID must be a UUID", i)
		assert.Equal(t, got.ID, item.OrderID, "item %d OrderID", i)
		assert.Equal(t, items[i].ProductID, item.ProductID)
		assert.Equal(t, items[i].ProductName, item.ProductName)
		assert.Equal(t, items[i].Quantity, item.Quantity)
		assert.Equal(t, items[i].UnitPrice, item.UnitPrice)

		_, dup := seen[item.ID]
		assert.False(t, dup, "item %d ID must be unique", i)
		seen[item.ID] = struct{}{}
	}
}

func TestNewOrder_UniqueIDs(t *testing.T) {
	t.Parallel()

	first, err := domain.NewOrder("user-1", "addr", []domain.OrderItem{{Quantity: 1, UnitPrice: 1}})
	require.NoError(t, err)
	second, err := domain.NewOrder("user-1", "addr", []domain.OrderItem{{Quantity: 1, UnitPrice: 1}})
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID)
}
