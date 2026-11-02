package order_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http/v1/order"
)

// benchItemCounts — размеры входа: типичный заказ и крупный.
func benchItemCounts() []int {
	return []int{1, 10}
}

// stubService отвечает готовым заказом без I/O: в бенчмарке измеряется только слой transport.
type stubService struct {
	order domain.Order
}

func (s stubService) CreateOrder(context.Context, service.CreateOrderInput) (domain.Order, error) {
	return s.order, nil
}

func benchCreateOrderRequest(items int) order.CreateOrderRequestDTO {
	dto := order.CreateOrderRequestDTO{
		AddressID: validUUID,
		Items:     make([]order.CreateOrderItemDTO, items),
	}
	for i := range dto.Items {
		dto.Items[i] = order.CreateOrderItemDTO{ProductID: validProductID, Quantity: 3}
	}
	return dto
}

func benchOrder(items int) domain.Order {
	result := domain.Order{
		ID:              validUUID,
		UserID:          validUUID,
		Status:          "pending",
		DeliveryAddress: "Москва, ул. Тверская, 1",
		Items:           make([]domain.OrderItem, items),
	}
	for i := range result.Items {
		result.Items[i] = domain.OrderItem{
			ID:          validUUID,
			ProductID:   validProductID,
			ProductName: "Товар",
			Quantity:    3,
			UnitPrice:   1000,
		}
		result.TotalAmount += 3 * 1000
	}
	return result
}

func BenchmarkCreateOrderRequestDTO_Validate(b *testing.B) {
	for _, items := range benchItemCounts() {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			dto := benchCreateOrderRequest(items)

			for b.Loop() {
				if err := dto.Validate(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkHandler_CreateOrder измеряет весь путь запроса в пакете: разбор JSON, Validate,
// маппинг в входные данные сервиса и обратно в ответ, запись JSON-конверта.
func BenchmarkHandler_CreateOrder(b *testing.B) {
	for _, items := range benchItemCounts() {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			body, err := json.Marshal(benchCreateOrderRequest(items))
			if err != nil {
				b.Fatal(err)
			}
			handler := order.NewOrderHandler(stubService{order: benchOrder(items)})

			for b.Loop() {
				req := httptest.NewRequest(http.MethodPost, ordersURL, bytes.NewReader(body))
				rec := httptest.NewRecorder()

				handler.CreateOrder(rec, req)

				if rec.Code != http.StatusCreated {
					b.Fatalf("status = %d, want %d", rec.Code, http.StatusCreated)
				}
			}
		})
	}
}
