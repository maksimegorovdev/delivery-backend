package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"

	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/service/mocks"
)

func TestOrderService_CreateOrder(t *testing.T) {
	t.Parallel()

	input := service.CreateOrderInput{
		UserID:    "user-1",
		AddressID: "address-1",
		Items: []service.CreateOrderItemInput{
			{ProductID: "p-1", Quantity: 2},
			{ProductID: "p-2", Quantity: 1},
		},
	}
	wantOrder := domain.Order{
		ID:     "order-1",
		UserID: "user-1",
		Status: domain.OrderStatusCreated,
		Items: []domain.OrderItem{
			{ID: "item-1", ProductID: "p-1", ProductName: "Milk", Quantity: 2, UnitPrice: 100},
		},
		TotalAmount:     200,
		DeliveryAddress: "Lenina 1",
		CreatedAt:       time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC),
	}

	t.Run("returns the order created by the provider", func(t *testing.T) {
		t.Parallel()

		orders := mocks.NewMockOrderProvider(t)
		orders.EXPECT().CreateOrder(t.Context(), input).Return(wantOrder, nil).Once()
		uc := service.NewOrderService(service.OrderServiceDeps{Orders: orders})

		got, err := uc.CreateOrder(t.Context(), input)

		require.NoError(t, err)
		assert.Equal(t, wantOrder, got)
	})

	t.Run("passes provider apperr through unchanged", func(t *testing.T) {
		t.Parallel()

		errProvider := apperr.NotFound().Wrap(errors.New("product not found"))
		orders := mocks.NewMockOrderProvider(t)
		orders.EXPECT().CreateOrder(t.Context(), input).Return(domain.Order{}, errProvider).Once()
		uc := service.NewOrderService(service.OrderServiceDeps{Orders: orders})

		got, err := uc.CreateOrder(t.Context(), input)

		require.ErrorIs(t, err, errProvider)
		appErr, ok := apperr.As(err)
		require.True(t, ok, "want apperr.Error, got %v", err)
		assert.Equal(t, apperr.CodeNotFound, appErr.Code)
		assert.Equal(t, domain.Order{}, got)
	})

	t.Run("passes plain provider error through", func(t *testing.T) {
		t.Parallel()

		errProvider := errors.New("provider unavailable")
		orders := mocks.NewMockOrderProvider(t)
		orders.EXPECT().CreateOrder(t.Context(), input).Return(domain.Order{}, errProvider).Once()
		uc := service.NewOrderService(service.OrderServiceDeps{Orders: orders})

		_, err := uc.CreateOrder(t.Context(), input)

		require.ErrorIs(t, err, errProvider)
	})
}
