package usecase

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
)

type CreateOrderItemInput struct {
	ProductID string
	Quantity  int32
}

type CreateOrderInput struct {
	UserID    string
	AddressID string
	Items     []CreateOrderItemInput
}

type OrderProvider interface {
	CreateOrder(ctx context.Context, input CreateOrderInput) (domain.Order, error)
}

type OrderUsecaseDeps struct {
	Orders OrderProvider
}

type OrderUsecase struct {
	orders OrderProvider
}

func NewOrderUsecase(deps OrderUsecaseDeps) *OrderUsecase {
	return &OrderUsecase{
		orders: deps.Orders,
	}
}

func (uc *OrderUsecase) CreateOrder(ctx context.Context, input CreateOrderInput) (domain.Order, error) {
	return uc.orders.CreateOrder(ctx, input)
}
