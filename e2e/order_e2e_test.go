//go:build e2e

package e2e_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// orderEventPayload — payload уведомления: событие order.created в том виде, в каком его пишет
// Debezium EventRouter. Суммы, цены и количества — JSON-числа, как в ответе API.
type orderEventPayload struct {
	OrderID         string           `json:"order_id"`
	UserID          string           `json:"user_id"`
	TotalAmount     int64            `json:"total_amount"`
	DeliveryAddress string           `json:"delivery_address"`
	Items           []orderEventItem `json:"items"`
}

// orderEventItem — позиция заказа в payload.
type orderEventItem struct {
	ID          string `json:"id"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	Quantity    int32  `json:"quantity"`
	UnitPrice   int64  `json:"unit_price"`
}

func TestCreateOrder_HappyPath(t *testing.T) {
	t.Parallel()

	const (
		borjomiQty = 2
		colaQty    = 1
	)

	order := createOrder(t, createOrderRequest{
		AddressID: seed.AddressID,
		Items: []orderItem{
			{ProductID: seed.Borjomi.ID, Quantity: borjomiQty},
			{ProductID: seed.Cola.ID, Quantity: colaQty},
		},
	})

	// Ответ API согласован с seed: пользователь, адрес, имена и цены из product.
	assert.Equal(t, seed.UserID, order.UserID)
	assert.Equal(t, seed.Address, order.DeliveryAddress)
	assert.Equal(t, int64(borjomiQty)*seed.Borjomi.Price+colaQty*seed.Cola.Price, order.TotalAmount)
	require.Len(t, order.Items, 2)

	want := map[string]struct {
		product  seedProduct
		quantity int32
	}{
		seed.Borjomi.ID: {seed.Borjomi, borjomiQty},
		seed.Cola.ID:    {seed.Cola, colaQty},
	}
	for _, item := range order.Items {
		w, ok := want[item.ProductID]
		if !assert.True(t, ok, "unexpected product %s in response", item.ProductID) {
			continue
		}

		assert.Equal(t, w.product.Name, item.ProductName)
		assert.Equal(t, w.product.Price, item.UnitPrice)
		assert.Equal(t, w.quantity, item.Quantity)
	}

	// Событие дошло через outbox → Debezium → Redpanda → notification ровно одной записью.
	inbox, notifications := awaitOrderEvent(t, order.ID)

	require.Len(t, inbox, 1)
	assert.Equal(t, eventTypeOrderCreated, inbox[0].EventType)
	assert.Equal(t, order.ID, inbox[0].AggregateID)

	require.Len(t, notifications, 1)
	assert.Equal(t, eventTypeOrderCreated, notifications[0].EventType)
	assert.Equal(t, channelLog, notifications[0].Channel)
	assert.Equal(t, inbox[0].EventID, notifications[0].EventID)

	var payload orderEventPayload
	require.NoError(t, json.Unmarshal(notifications[0].Payload, &payload))

	assert.Equal(t, order.ID, payload.OrderID)
	assert.Equal(t, order.UserID, payload.UserID)
	assert.Equal(t, order.TotalAmount, payload.TotalAmount)
	assert.Equal(t, order.DeliveryAddress, payload.DeliveryAddress)
	assert.ElementsMatch(t, eventItems(order.Items), payload.Items)
}

// eventItems переводит позиции ответа API в вид payload: набор полей у них совпадает.
func eventItems(items []orderItemResponse) []orderEventItem {
	result := make([]orderEventItem, 0, len(items))
	for _, item := range items {
		result = append(result, orderEventItem(item))
	}

	return result
}
