package event_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/event"
)

func benchOrder(n int) domain.Order {
	items := make([]domain.OrderItem, n)
	for i := range items {
		items[i] = domain.OrderItem{
			ID:          "0190a000-0000-7000-8000-000000000201",
			OrderID:     "0190a000-0000-7000-8000-000000000101",
			ProductID:   "0190a000-0000-7000-8000-0000000000a1",
			ProductName: "Bread",
			Quantity:    2,
			UnitPrice:   5000,
		}
	}

	return domain.Order{
		ID:              "0190a000-0000-7000-8000-000000000101",
		UserID:          "0190a000-0000-7000-8000-000000000001",
		Status:          domain.OrderStatusCreated,
		Items:           items,
		TotalAmount:     int64(n) * 10000,
		DeliveryAddress: "Lenina st, 1",
		CreatedAt:       time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
	}
}

func BenchmarkOrderEventBuilder_OrderCreated(b *testing.B) {
	for _, n := range []int{1, 10} {
		b.Run(fmt.Sprintf("items=%d", n), func(b *testing.B) {
			builder := event.NewOrderEventBuilder()
			order := benchOrder(n)

			b.ReportAllocs()
			for b.Loop() {
				if _, err := builder.OrderCreated(order); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
