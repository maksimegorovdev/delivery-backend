//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
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

// Коды и причины ошибок в конверте gateway. Значения заданы контрактом platform/apperr
// (code.go, reason.go); модуль e2e не импортирует platform, поэтому они повторены здесь.
const (
	codeInvalidArgument = "INVALID_ARGUMENT"
	codeNotFound        = "NOT_FOUND"

	reasonInvalidRequestBody = "INVALID_REQUEST_BODY"
	reasonValidationFailed   = "VALIDATION_FAILED"

	// missingID — корректный UUID, которого нет ни в product, ни в user.
	missingID = "00000000-0000-4000-8000-000000000001"
)

func TestCreateOrder_ClientErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantReason string
	}{
		{
			name:       "invalid json",
			body:       `{"address_id":`,
			wantReason: reasonInvalidRequestBody,
		},
		{
			name:       "missing items",
			body:       fmt.Sprintf(`{"address_id":%q}`, seed.AddressID),
			wantReason: reasonValidationFailed,
		},
		{
			name: "address id is not a uuid",
			body: fmt.Sprintf(
				`{"address_id":"not-a-uuid","items":[{"product_id":%q,"quantity":1}]}`,
				seed.Borjomi.ID,
			),
			wantReason: reasonValidationFailed,
		},
		{
			name: "zero quantity",
			body: fmt.Sprintf(
				`{"address_id":%q,"items":[{"product_id":%q,"quantity":0}]}`,
				seed.AddressID, seed.Borjomi.ID,
			),
			wantReason: reasonValidationFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, env, err := postOrder(t.Context(), []byte(tt.body))
			require.NoError(t, err)

			assert.Equal(t, http.StatusBadRequest, status)
			require.NotNil(t, env.Error, "response has no error")
			assert.Equal(t, codeInvalidArgument, env.Error.Code)
			assert.Equal(t, tt.wantReason, env.Error.Reason)
		})
	}
}

func TestCreateOrder_NotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  createOrderRequest
	}{
		{
			name: "unknown product",
			req: createOrderRequest{
				AddressID: seed.AddressID,
				Items:     []orderItem{{ProductID: missingID, Quantity: 1}},
			},
		},
		{
			name: "unknown address",
			req: createOrderRequest{
				AddressID: missingID,
				Items:     []orderItem{{ProductID: seed.Borjomi.ID, Quantity: 1}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, err := json.Marshal(tt.req)
			require.NoError(t, err)

			status, env, err := postOrder(t.Context(), body)
			require.NoError(t, err)

			assert.Equal(t, http.StatusNotFound, status)
			require.NotNil(t, env.Error, "response has no error")
			assert.Equal(t, codeNotFound, env.Error.Code)
			assert.Contains(t, []string{"", "null"}, string(env.Data), "order must not be created")
		})
	}
}

// eventItems переводит позиции ответа API в вид payload: набор полей у них совпадает.
func eventItems(items []orderItemResponse) []orderEventItem {
	result := make([]orderEventItem, 0, len(items))
	for _, item := range items {
		result = append(result, orderEventItem(item))
	}

	return result
}
