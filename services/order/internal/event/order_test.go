package event_test

import (
	"encoding/json"
	"testing"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/event"
)

func fixtureOrder() domain.Order {
	return domain.Order{
		ID:     "0192f0c1-0000-7000-8000-000000000001",
		UserID: "user-1",
		Status: domain.OrderStatusCreated,
		Items: []domain.OrderItem{
			{ID: "item-1", OrderID: "order-1", ProductID: "p-1", ProductName: "Milk", Quantity: 2, UnitPrice: 100},
			{ID: "item-2", OrderID: "order-1", ProductID: "p-2", ProductName: "Bread", Quantity: 1, UnitPrice: 50},
			{ID: "item-3", OrderID: "order-1", ProductID: "p-3", ProductName: "Eggs", Quantity: 12, UnitPrice: 15},
		},
		TotalAmount:     430,
		DeliveryAddress: "Lenina 1",
		CreatedAt:       time.Date(2026, time.October, 30, 12, 30, 15, 123456000, time.UTC),
		UpdatedAt:       time.Date(2026, time.October, 30, 12, 30, 15, 123456000, time.UTC),
	}
}

func TestOrderEventBuilder_OrderCreated(t *testing.T) {
	t.Parallel()

	order := fixtureOrder()

	got, err := event.NewOrderEventBuilder().OrderCreated(order)

	require.NoError(t, err)

	_, err = uuid.Parse(got.ID)
	require.NoError(t, err, "event ID must be a UUID")

	assert.Equal(t, domain.AggregateTypeOrder, got.AggregateType)
	assert.Equal(t, order.ID, got.AggregateID)
	assert.Equal(t, domain.EventTypeOrderCreated, got.Type)
	assert.Equal(t, order.CreatedAt, got.CreatedAt)
	assert.NotEmpty(t, got.Payload)
}

func TestOrderEventBuilder_OrderCreated_Payload(t *testing.T) {
	t.Parallel()

	order := fixtureOrder()

	got, err := event.NewOrderEventBuilder().OrderCreated(order)
	require.NoError(t, err)

	var payload orderv1.OrderCreated
	require.NoError(t, protojson.Unmarshal(got.Payload, &payload))

	assert.Equal(t, got.ID, payload.GetEventId())
	assert.Equal(t, order.ID, payload.GetOrderId())
	assert.Equal(t, order.UserID, payload.GetUserId())
	assert.Equal(t, order.TotalAmount, payload.GetTotalAmount())
	assert.Equal(t, order.DeliveryAddress, payload.GetDeliveryAddress())
	assert.True(t, order.CreatedAt.Equal(payload.GetCreatedAt().AsTime()), "CreatedAt must survive the round trip")

	require.Len(t, payload.GetItems(), len(order.Items))
	for i, want := range order.Items {
		item := payload.GetItems()[i]
		assert.Equal(t, want.ID, item.GetId(), "item %d id", i)
		assert.Equal(t, want.ProductID, item.GetProductId(), "item %d product_id", i)
		assert.Equal(t, want.ProductName, item.GetProductName(), "item %d product_name", i)
		assert.Equal(t, want.Quantity, item.GetQuantity(), "item %d quantity", i)
		assert.Equal(t, want.UnitPrice, item.GetUnitPrice(), "item %d unit_price", i)
	}
}

func TestOrderEventBuilder_OrderCreated_SnakeCaseFields(t *testing.T) {
	t.Parallel()

	got, err := event.NewOrderEventBuilder().OrderCreated(fixtureOrder())
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(got.Payload, &raw))

	for _, field := range []string{
		"event_id", "order_id", "user_id", "items", "total_amount", "delivery_address", "created_at",
	} {
		assert.Contains(t, raw, field)
	}
	for _, field := range []string{"eventId", "orderId", "userId", "totalAmount", "deliveryAddress", "createdAt"} {
		assert.NotContains(t, raw, field, "payload must use proto names, not lowerCamelCase")
	}

	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["items"], &items))
	require.NotEmpty(t, items)
	for _, field := range []string{"id", "product_id", "product_name", "quantity", "unit_price"} {
		assert.Contains(t, items[0], field)
	}
}

func TestOrderEventBuilder_OrderCreated_NoItems(t *testing.T) {
	t.Parallel()

	order := fixtureOrder()
	order.Items = nil
	order.TotalAmount = 0

	got, err := event.NewOrderEventBuilder().OrderCreated(order)
	require.NoError(t, err)

	var payload orderv1.OrderCreated
	require.NoError(t, protojson.Unmarshal(got.Payload, &payload))
	assert.Empty(t, payload.GetItems())
	assert.Equal(t, order.ID, payload.GetOrderId())
}

func TestOrderEventBuilder_OrderCreated_UniqueEventIDs(t *testing.T) {
	t.Parallel()

	builder := event.NewOrderEventBuilder()

	first, err := builder.OrderCreated(fixtureOrder())
	require.NoError(t, err)
	second, err := builder.OrderCreated(fixtureOrder())
	require.NoError(t, err)

	assert.NotEqual(t, first.ID, second.ID)
}

func TestOrderEventBuilder_OrderCreated_InvalidUTF8(t *testing.T) {
	t.Parallel()

	order := fixtureOrder()
	order.DeliveryAddress = "\xff\xfe"

	got, err := event.NewOrderEventBuilder().OrderCreated(order)

	require.Error(t, err)
	appErr, ok := apperr.As(err)
	require.True(t, ok, "want apperr.Error, got %v", err)
	assert.Equal(t, apperr.CodeInternal, appErr.Code)
	assert.Equal(t, domain.OutboxEvent{}, got)
}

func FuzzOrderCreated(f *testing.F) {
	f.Add("user-1", "Lenina 1", "p-1", "Milk", int32(2), int64(100), uint8(3))
	f.Add("", "", "", "", int32(0), int64(0), uint8(0))
	f.Add("u", "addr", "p", "name", int32(-1), int64(-1), uint8(1))
	f.Add("юзер", "ул. Ленина, 1", "п-1", "Молоко", int32(1<<30), int64(1<<40), uint8(255))
	f.Add("user", "\xff\xfe", "p", "n", int32(1), int64(1), uint8(1))

	f.Fuzz(func(
		t *testing.T,
		userID, address, productID, productName string,
		quantity int32,
		unitPrice int64,
		count uint8,
	) {
		items := make([]domain.OrderItem, 0, count)
		for i := range count {
			items = append(items, domain.OrderItem{
				ID:          uuid.NewV7().String(),
				ProductID:   productID,
				ProductName: productName,
				Quantity:    quantity + int32(i),
				UnitPrice:   unitPrice + int64(i),
			})
		}
		order := domain.Order{
			ID:              uuid.NewV7().String(),
			UserID:          userID,
			Items:           items,
			TotalAmount:     unitPrice,
			DeliveryAddress: address,
			CreatedAt:       time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC),
		}

		got, err := event.NewOrderEventBuilder().OrderCreated(order)

		// Строки позиций попадают в payload только при count > 0.
		validUTF8 := utf8.ValidString(userID) && utf8.ValidString(address)
		if count > 0 {
			validUTF8 = validUTF8 && utf8.ValidString(productID) && utf8.ValidString(productName)
		}
		if !validUTF8 {
			require.Error(t, err)
			_, ok := apperr.As(err)
			require.True(t, ok, "want apperr.Error, got %v", err)
			return
		}
		require.NoError(t, err)

		var payload orderv1.OrderCreated
		require.NoError(t, protojson.Unmarshal(got.Payload, &payload))

		require.Equal(t, got.ID, payload.GetEventId())
		require.Equal(t, order.ID, payload.GetOrderId())
		require.Equal(t, order.UserID, payload.GetUserId())
		require.Equal(t, order.TotalAmount, payload.GetTotalAmount())
		require.Equal(t, order.DeliveryAddress, payload.GetDeliveryAddress())
		require.Len(t, payload.GetItems(), len(order.Items))
		for i, want := range order.Items {
			item := payload.GetItems()[i]
			require.Equal(t, want.ID, item.GetId())
			require.Equal(t, want.ProductID, item.GetProductId())
			require.Equal(t, want.ProductName, item.GetProductName())
			require.Equal(t, want.Quantity, item.GetQuantity())
			require.Equal(t, want.UnitPrice, item.GetUnitPrice())
		}
	})
}
