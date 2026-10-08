package order

import (
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/service"
)

func toCreateOrderInput(userID string, dto CreateOrderRequestDTO) service.CreateOrderInput {
	items := make([]service.CreateOrderItemInput, 0, len(dto.Items))
	for _, item := range dto.Items {
		items = append(items, service.CreateOrderItemInput{
			ProductID: item.ProductId,
			Quantity:  item.Quantity,
		})
	}
	return service.CreateOrderInput{
		UserID:    userID,
		AddressID: dto.AddressID,
		Items:     items,
	}
}

func toOrderResponseDTO(order domain.Order) OrderResponseDTO {
	items := make([]OrderItemResponseDTO, 0, len(order.Items))
	for _, item := range order.Items {
		items = append(items, OrderItemResponseDTO{
			ID:          item.ID,
			ProductID:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice:   item.UnitPrice,
		})
	}
	return OrderResponseDTO{
		ID:              order.ID,
		UserID:          order.UserID,
		Status:          string(order.Status),
		Items:           items,
		TotalAmount:     order.TotalAmount,
		DeliveryAddress: order.DeliveryAddress,
		CreatedAt:       order.CreatedAt,
		UpdatedAt:       order.UpdatedAt,
	}
}
