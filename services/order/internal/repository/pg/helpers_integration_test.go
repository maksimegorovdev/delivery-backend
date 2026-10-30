//go:build integration

package pg_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

const migrationsDir = "../../../migrations/pg"

// Фиксированные идентификаторы собственных фикстур: на seed-миграции тесты не опираются.
const (
	fixtureOrderID   = "0190a000-0000-7000-8000-000000000101"
	fixtureUserID    = "0190a000-0000-7000-8000-000000000001"
	fixtureItemID    = "0190a000-0000-7000-8000-000000000201"
	fixtureProductID = "0190a000-0000-7000-8000-0000000000a1"
	fixtureEventID   = "0190a000-0000-7000-8000-000000000301"
)

func TestMain(m *testing.M) {
	// Без propagator'а OutboxRepo.Save не запишет trace-контекст: глобальный по умолчанию пустой.
	otel.SetTextMapPropagator(propagation.TraceContext{})

	m.Run()
}

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

// fixtureOrder возвращает заказ с одной позицией и фиксированными идентификаторами.
func fixtureOrder() domain.Order {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	return domain.Order{
		ID:              fixtureOrderID,
		UserID:          fixtureUserID,
		Status:          domain.OrderStatusCreated,
		TotalAmount:     10000,
		DeliveryAddress: "Lenina st, 1",
		CreatedAt:       at,
		UpdatedAt:       at,
		Items: []domain.OrderItem{{
			ID:          fixtureItemID,
			OrderID:     fixtureOrderID,
			ProductID:   fixtureProductID,
			ProductName: "Bread",
			Quantity:    2,
			UnitPrice:   5000,
		}},
	}
}

// fixtureEvent возвращает outbox-событие с фиксированными идентификаторами.
func fixtureEvent() domain.OutboxEvent {
	return domain.OutboxEvent{
		ID:            fixtureEventID,
		AggregateType: "order",
		AggregateID:   fixtureOrderID,
		Type:          "order.created",
		Payload:       []byte(`{"order_id":"` + fixtureOrderID + `"}`),
		CreatedAt:     time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}
