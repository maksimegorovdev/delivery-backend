package domain

import (
	"time"
	"uuid"
)

type OrderStatus string

const (
	OrderStatusCreated         OrderStatus = "created"
	OrderStatusPaid            OrderStatus = "paid"
	OrderStatusConfirmed       OrderStatus = "confirmed"
	OrderStatusAssembling      OrderStatus = "assembling"
	OrderStatusAssembled       OrderStatus = "assembled"
	OrderStatusCourierAssigned OrderStatus = "courier_assigned"
	OrderStatusDelivering      OrderStatus = "delivering"
	OrderStatusDelivered       OrderStatus = "delivered"
	OrderStatusCanceled        OrderStatus = "canceled"
)

type OrderItem struct {
	ID          string
	OrderID     string
	ProductID   string
	ProductName string
	Quantity    int32
	UnitPrice   int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (i OrderItem) Subtotal() int64 {
	return i.UnitPrice * int64(i.Quantity)
}

type Order struct {
	ID              string
	UserID          string
	Status          OrderStatus
	Items           []OrderItem
	TotalAmount     int64
	DeliveryAddress string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func NewOrder(userID string, deliveryAddress string, items []OrderItem) (Order, error) {
	if len(items) == 0 {
		return Order{}, ErrOrderNoItems
	}

	id := uuid.NewV7().String()
	now := time.Now().UTC()
	var total int64
	for i := range items {
		items[i].ID = uuid.NewV7().String()
		items[i].OrderID = id
		items[i].CreatedAt = now
		items[i].UpdatedAt = now
		total += items[i].Subtotal()
	}

	return Order{
		ID:              id,
		UserID:          userID,
		Status:          OrderStatusCreated,
		Items:           items,
		TotalAmount:     total,
		DeliveryAddress: deliveryAddress,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}
