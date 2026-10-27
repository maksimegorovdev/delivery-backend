package order

import (
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/service"
)

func toCreateOrderInput(userID string, dto CreateOrderRequestDTO) service.CreateOrderInput {
	items := make([]service.CreateOrderItemInput, 0, len(dto.Items))
	for _, item := range dto.Items {
		items = append(items, service.CreateOrderItemInput{
			ProductID: item.ProductID,
			Quantity:  item.Quantity,
		})
	}
	return service.CreateOrderInput{
		UserID:    userID,
		AddressID: dto.AddressID,
		Items:     items,
	}
}

func toResponseDTO(order domain.Order) ResponseDTO {
	items := make([]ItemResponseDTO, 0, len(order.Items))
	for _, item := range order.Items {
		items = append(items, ItemResponseDTO{
			ID:          item.ID,
			ProductID:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice:   item.UnitPrice,
		})
	}
	return ResponseDTO{
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
