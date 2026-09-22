package client

import (
	"context"

	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/grpcerr"
	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/usecase"
)

type OrderClient struct {
	orders orderv1.OrderServiceClient
}

func NewOrderClient(conn *grpc.ClientConn) *OrderClient {
	return &OrderClient{
		orders: orderv1.NewOrderServiceClient(conn),
	}
}

func (c *OrderClient) CreateOrder(ctx context.Context, input usecase.CreateOrderInput) (domain.Order, error) {
	items := make([]*orderv1.CreateOrderItem, 0, len(input.Items))
	for _, item := range input.Items {
		items = append(items, &orderv1.CreateOrderItem{
			ProductId: item.ProductID,
			Quantity:  item.Quantity,
		})
	}

	resp, err := c.orders.CreateOrder(ctx, &orderv1.CreateOrderRequest{
		UserId:    input.UserID,
		AddressId: input.AddressID,
		Items:     items,
	})
	if err != nil {
		return domain.Order{}, grpcerr.Map(err)
	}
	return protoToOrder(resp.GetOrder()), nil
}

func protoToOrder(p *orderv1.Order) domain.Order {
	items := make([]domain.OrderItem, 0, len(p.Items))
	for _, item := range p.Items {
		items = append(items, domain.OrderItem{
			ID:          item.GetId(),
			ProductID:   item.GetProductId(),
			ProductName: item.GetProductName(),
			Quantity:    item.GetQuantity(),
			UnitPrice:   item.GetUnitPrice(),
		})
	}

	return domain.Order{
		ID:              p.GetId(),
		UserID:          p.GetUserId(),
		Status:          statusFromProto(p.GetStatus()),
		Items:           items,
		TotalAmount:     p.GetTotalAmount(),
		DeliveryAddress: p.GetDeliveryAddress(),
		CreatedAt:       p.GetCreatedAt().AsTime(),
		UpdatedAt:       p.GetUpdatedAt().AsTime(),
	}
}

func statusFromProto(status orderv1.OrderStatus) domain.OrderStatus {
	switch status {
	case orderv1.OrderStatus_ORDER_STATUS_CREATED:
		return domain.OrderStatusCreated
	case orderv1.OrderStatus_ORDER_STATUS_PAID:
		return domain.OrderStatusPaid
	case orderv1.OrderStatus_ORDER_STATUS_CONFIRMED:
		return domain.OrderStatusConfirmed
	case orderv1.OrderStatus_ORDER_STATUS_ASSEMBLING:
		return domain.OrderStatusAssembling
	case orderv1.OrderStatus_ORDER_STATUS_ASSEMBLED:
		return domain.OrderStatusAssembled
	case orderv1.OrderStatus_ORDER_STATUS_COURIER_ASSIGNED:
		return domain.OrderStatusCourierAssigned
	case orderv1.OrderStatus_ORDER_STATUS_DELIVERING:
		return domain.OrderStatusDelivering
	case orderv1.OrderStatus_ORDER_STATUS_DELIVERED:
		return domain.OrderStatusDelivered
	case orderv1.OrderStatus_ORDER_STATUS_CANCELED:
		return domain.OrderStatusCanceled
	default:
		return domain.OrderStatusUnspecified
	}
}
