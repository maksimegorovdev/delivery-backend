package order_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/ozzoerr"

	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http/v1/order"
)

const (
	validUUID      = "0192f0c1-0000-7000-8000-000000000001"
	validProductID = "0192f0c1-0000-7000-8000-0000000000aa"
)

func TestCreateOrderRequestDTO_Validate(t *testing.T) {
	t.Parallel()

	validItem := order.CreateOrderItemDTO{ProductID: validProductID, Quantity: 1}

	tests := []struct {
		name       string
		dto        order.CreateOrderRequestDTO
		wantFields []string // пусто — DTO валиден
	}{
		{
			name: "valid request",
			dto:  order.CreateOrderRequestDTO{AddressID: validUUID, Items: []order.CreateOrderItemDTO{validItem}},
		},
		{
			name: "valid request with several items",
			dto: order.CreateOrderRequestDTO{
				AddressID: validUUID,
				Items:     []order.CreateOrderItemDTO{validItem, {ProductID: validUUID, Quantity: 5}},
			},
		},
		{
			name:       "empty address",
			dto:        order.CreateOrderRequestDTO{Items: []order.CreateOrderItemDTO{validItem}},
			wantFields: []string{"address_id"},
		},
		{
			name: "address is not a uuid",
			dto: order.CreateOrderRequestDTO{
				AddressID: "address-1",
				Items:     []order.CreateOrderItemDTO{validItem},
			},
			wantFields: []string{"address_id"},
		},
		{
			name:       "nil items",
			dto:        order.CreateOrderRequestDTO{AddressID: validUUID},
			wantFields: []string{"items"},
		},
		{
			name:       "empty items",
			dto:        order.CreateOrderRequestDTO{AddressID: validUUID, Items: []order.CreateOrderItemDTO{}},
			wantFields: []string{"items"},
		},
		{
			name: "item without product id",
			dto: order.CreateOrderRequestDTO{
				AddressID: validUUID,
				Items:     []order.CreateOrderItemDTO{{Quantity: 1}},
			},
			wantFields: []string{"items.0.product_id"},
		},
		{
			name: "item product id is not a uuid",
			dto: order.CreateOrderRequestDTO{
				AddressID: validUUID,
				Items:     []order.CreateOrderItemDTO{{ProductID: "p-1", Quantity: 1}},
			},
			wantFields: []string{"items.0.product_id"},
		},
		{
			name: "item with zero quantity",
			dto: order.CreateOrderRequestDTO{
				AddressID: validUUID,
				Items:     []order.CreateOrderItemDTO{{ProductID: validProductID}},
			},
			wantFields: []string{"items.0.quantity"},
		},
		{
			name: "item with negative quantity",
			dto: order.CreateOrderRequestDTO{
				AddressID: validUUID,
				Items:     []order.CreateOrderItemDTO{{ProductID: validProductID, Quantity: -3}},
			},
			wantFields: []string{"items.0.quantity"},
		},
		{
			name: "violation is reported for the broken item only",
			dto: order.CreateOrderRequestDTO{
				AddressID: validUUID,
				Items:     []order.CreateOrderItemDTO{validItem, {ProductID: validProductID, Quantity: 0}},
			},
			wantFields: []string{"items.1.quantity"},
		},
		{
			name: "all fields broken",
			dto: order.CreateOrderRequestDTO{
				AddressID: "x",
				Items:     []order.CreateOrderItemDTO{{ProductID: "y"}},
			},
			wantFields: []string{"address_id", "items.0.product_id", "items.0.quantity"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.dto.Validate()

			if len(tt.wantFields) == 0 {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var gotFields []string
			for _, v := range ozzoerr.Flatten("", err) {
				gotFields = append(gotFields, v.Field)
			}
			assert.Equal(t, tt.wantFields, gotFields)
		})
	}
}

func TestCreateOrderItemDTO_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dto        order.CreateOrderItemDTO
		wantFields []string
	}{
		{name: "valid item", dto: order.CreateOrderItemDTO{ProductID: validProductID, Quantity: 1}},
		{name: "large quantity", dto: order.CreateOrderItemDTO{ProductID: validProductID, Quantity: 1 << 30}},
		{name: "empty item", dto: order.CreateOrderItemDTO{}, wantFields: []string{"product_id", "quantity"}},
		{
			name:       "bad product id",
			dto:        order.CreateOrderItemDTO{ProductID: "p", Quantity: 1},
			wantFields: []string{"product_id"},
		},
		{
			name:       "zero quantity",
			dto:        order.CreateOrderItemDTO{ProductID: validProductID},
			wantFields: []string{"quantity"},
		},
		{
			name:       "negative quantity",
			dto:        order.CreateOrderItemDTO{ProductID: validProductID, Quantity: -1},
			wantFields: []string{"quantity"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.dto.Validate()

			if len(tt.wantFields) == 0 {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			var gotFields []string
			for _, v := range ozzoerr.Flatten("", err) {
				gotFields = append(gotFields, v.Field)
			}
			assert.Equal(t, tt.wantFields, gotFields)
		})
	}
}
