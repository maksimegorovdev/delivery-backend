package domain_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

func benchItems(n int) []domain.OrderItem {
	items := make([]domain.OrderItem, n)
	for i := range items {
		items[i] = domain.OrderItem{
			ProductID:   "01a0d1fa-a4e8-75a1-802d-d6e3c642b0f4",
			ProductName: "Bread",
			Quantity:    3,
			UnitPrice:   5000,
		}
	}

	return items
}

func BenchmarkNewOrder(b *testing.B) {
	for _, n := range []int{1, 10} {
		b.Run(fmt.Sprintf("items=%d", n), func(b *testing.B) {
			items := benchItems(n)

			b.ReportAllocs()
			for b.Loop() {
				// NewOrder записывает ID в переданные позиции, поэтому на каждой итерации — своя копия.
				if _, err := domain.NewOrder("user-1", "Lenina st, 1", slices.Clone(items)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkOrderItem_Subtotal(b *testing.B) {
	item := domain.OrderItem{Quantity: 3, UnitPrice: 5000}

	b.ReportAllocs()
	for b.Loop() {
		_ = item.Subtotal()
	}
}
