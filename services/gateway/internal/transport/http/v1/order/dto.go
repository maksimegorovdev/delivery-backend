package order

import (
	"time"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

type CreateOrderItemDTO struct {
	ProductId string `json:"product_id"`
	Quantity  int32  `json:"quantity"`
}

func (d CreateOrderItemDTO) Validate() error {
	return validation.ValidateStruct(&d,
		validation.Field(&d.ProductId, validation.Required, is.UUID),
		validation.Field(&d.Quantity, validation.Required, validation.Min(1)),
	)
}

type CreateOrderRequestDTO struct {
	AddressID string               `json:"address_id"`
	Items     []CreateOrderItemDTO `json:"items"`
}

func (d CreateOrderRequestDTO) Validate() error {
	return validation.ValidateStruct(&d,
		validation.Field(&d.AddressID, validation.Required, is.UUID),
		validation.Field(&d.Items, validation.Required, validation.Length(1, 0)),
	)
}

type OrderItemResponseDTO struct {
	ID          string `json:"id"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	Quantity    int32  `json:"quantity"`
	UnitPrice   int64  `json:"unit_price"`
}

type OrderResponseDTO struct {
	ID              string                 `json:"id"`
	UserID          string                 `json:"user_id"`
	Status          string                 `json:"status"`
	Items           []OrderItemResponseDTO `json:"items"`
	TotalAmount     int64                  `json:"total_amount"`
	DeliveryAddress string                 `json:"delivery_address"`
	CreatedAt       time.Time              `json:"created_at"`
	UpdatedAt       time.Time              `json:"updated_at"`
}
