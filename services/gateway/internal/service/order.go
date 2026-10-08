package service

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

type OrderServiceDeps struct {
	Orders OrderProvider
}

type OrderService struct {
	orders OrderProvider
}

func NewOrderService(deps OrderServiceDeps) *OrderService {
	return &OrderService{
		orders: deps.Orders,
	}
}

func (uc *OrderService) CreateOrder(ctx context.Context, input CreateOrderInput) (domain.Order, error) {
	return uc.orders.CreateOrder(ctx, input)
}
