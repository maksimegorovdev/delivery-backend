package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/service/mocks"
)

// benchTx выполняет fn сразу, как транзакция без БД.
type benchTx struct{}

func (benchTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

func BenchmarkOrderService_CreateOrder(b *testing.B) {
	for _, n := range []int{1, 10} {
		b.Run(fmt.Sprintf("items=%d", n), func(b *testing.B) {
			input := service.CreateOrderInput{UserID: "user-1", AddressID: "address-1"}
			ids := make([]string, n)
			products := make([]domain.Product, n)
			for i := range n {
				ids[i] = fmt.Sprintf("product-%d", i)
				products[i] = domain.Product{ID: ids[i], Name: "Bread", Price: 5000}
				input.Items = append(input.Items, service.CreateOrderItemInput{ProductID: ids[i], Quantity: 2})
			}

			users := mocks.NewMockUserProvider(b)
			users.EXPECT().GetUser(mock.Anything, "user-1").Return(domain.User{ID: "user-1"}, nil)
			users.EXPECT().GetAddress(mock.Anything, "address-1").
				Return(domain.Address{ID: "address-1", Address: "Lenina st, 1"}, nil)
			catalog := mocks.NewMockProductProvider(b)
			catalog.EXPECT().GetProducts(mock.Anything, ids).Return(products, nil)
			events := mocks.NewMockOrderEvents(b)
			events.EXPECT().OrderCreated(mock.Anything).Return(domain.OutboxEvent{ID: "event-1"}, nil)
			orders := mocks.NewMockOrderRepo(b)
			orders.EXPECT().Create(mock.Anything, mock.Anything).Return(nil)
			outbox := mocks.NewMockOutboxRepo(b)
			outbox.EXPECT().Save(mock.Anything, mock.Anything).Return(nil)

			svc := service.NewOrderService(service.OrderServiceDeps{
				Tx: benchTx{}, Orders: orders, Outbox: outbox, Events: events, Users: users, Products: catalog,
			})
			ctx := b.Context()

			b.ReportAllocs()
			for b.Loop() {
				if _, err := svc.CreateOrder(ctx, input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
