package grpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/usecase"
)

type OrderUsecase interface {
	CreateOrder(ctx context.Context, input usecase.CreateOrderInput) (domain.Order, error)
}

func statusToProto(status domain.OrderStatus) orderv1.OrderStatus {
	switch status {
	case domain.OrderStatusCreated:
		return orderv1.OrderStatus_ORDER_STATUS_CREATED
	case domain.OrderStatusPaid:
		return orderv1.OrderStatus_ORDER_STATUS_PAID
	case domain.OrderStatusConfirmed:
		return orderv1.OrderStatus_ORDER_STATUS_CONFIRMED
	case domain.OrderStatusAssembling:
		return orderv1.OrderStatus_ORDER_STATUS_ASSEMBLING
	case domain.OrderStatusAssembled:
		return orderv1.OrderStatus_ORDER_STATUS_ASSEMBLED
	case domain.OrderStatusCourierAssigned:
		return orderv1.OrderStatus_ORDER_STATUS_COURIER_ASSIGNED
	case domain.OrderStatusDelivering:
		return orderv1.OrderStatus_ORDER_STATUS_DELIVERING
	case domain.OrderStatusDelivered:
		return orderv1.OrderStatus_ORDER_STATUS_DELIVERED
	case domain.OrderStatusCanceled:
		return orderv1.OrderStatus_ORDER_STATUS_CANCELED
	default:
		return orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED
	}
}

func orderToProto(o domain.Order) *orderv1.Order {
	items := make([]*orderv1.OrderItem, 0, len(o.Items))
	for _, item := range o.Items {
		items = append(items, &orderv1.OrderItem{
			Id:          item.ID,
			ProductId:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice:   item.UnitPrice,
			CreatedAt:   timestamppb.New(item.CreatedAt),
			UpdatedAt:   timestamppb.New(item.UpdatedAt),
		})
	}
	return &orderv1.Order{
		Id:              o.ID,
		UserId:          o.UserID,
		Status:          statusToProto(o.Status),
		Items:           items,
		TotalAmount:     o.TotalAmount,
		DeliveryAddress: o.DeliveryAddress,
		CreatedAt:       timestamppb.New(o.CreatedAt),
		UpdatedAt:       timestamppb.New(o.UpdatedAt),
	}
}

type OrderRouter struct {
	orderv1.OrderServiceServer
	uc OrderUsecase
}

func NewOrderRouter(server *grpc.Server, uc OrderUsecase) {
	orderv1.RegisterOrderServiceServer(server, &OrderRouter{uc: uc})
}

func (r *OrderRouter) CreateOrder(ctx context.Context, req *orderv1.CreateOrderRequest) (*orderv1.CreateOrderResponse, error) {
	input := usecase.CreateOrderInput{
		UserID:    req.GetUserId(),
		AddressID: req.GetAddressId(),
		Items:     make([]usecase.CreateOrderItemInput, 0, len(req.GetItems())),
	}
	for _, item := range req.GetItems() {
		input.Items = append(input.Items, usecase.CreateOrderItemInput{
			ProductID: item.ProductId,
			Quantity:  item.Quantity,
		})
	}

	order, err := r.uc.CreateOrder(ctx, input)
	if err != nil {
		return nil, err
	}
	return &orderv1.CreateOrderResponse{Order: orderToProto(order)}, nil
}
