package domain

import "time"

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
