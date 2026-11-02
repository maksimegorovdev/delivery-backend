package service_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/service"
)

// Зависимости бенчмарка — заглушки с готовыми ответами, а не моки testify: мок на каждом вызове
// сопоставляет аргументы через reflection, снимает стек и копит вызовы в Calls, и бенчмарк
// измерял бы testify, а не сервис.

// benchTx выполняет fn сразу, как транзакция без БД.
type benchTx struct{}

func (benchTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

type benchUsers struct {
	user    domain.User
	address domain.Address
}

func (u benchUsers) GetUser(context.Context, string) (domain.User, error) { return u.user, nil }

func (u benchUsers) GetAddress(context.Context, string) (domain.Address, error) {
	return u.address, nil
}

type benchProducts struct {
	products []domain.Product
}

func (p benchProducts) GetProducts(context.Context, []string) ([]domain.Product, error) {
	return p.products, nil
}

type benchEvents struct {
	event domain.OutboxEvent
}

func (e benchEvents) OrderCreated(domain.Order) (domain.OutboxEvent, error) { return e.event, nil }

type benchOrders struct{}

func (benchOrders) Create(context.Context, domain.Order) error { return nil }

type benchOutbox struct{}

func (benchOutbox) Save(context.Context, domain.OutboxEvent) error { return nil }

func BenchmarkOrderService_CreateOrder(b *testing.B) {
	for _, n := range []int{1, 10} {
		b.Run(fmt.Sprintf("items=%d", n), func(b *testing.B) {
			input := service.CreateOrderInput{UserID: "user-1", AddressID: "address-1"}
			products := make([]domain.Product, n)
			for i := range n {
				id := fmt.Sprintf("product-%d", i)
				products[i] = domain.Product{ID: id, Name: "Bread", Price: 5000}
				input.Items = append(input.Items, service.CreateOrderItemInput{ProductID: id, Quantity: 2})
			}

			svc := service.NewOrderService(service.OrderServiceDeps{
				Tx:     benchTx{},
				Orders: benchOrders{},
				Outbox: benchOutbox{},
				Events: benchEvents{event: domain.OutboxEvent{ID: "event-1"}},
				Users: benchUsers{
					user:    domain.User{ID: "user-1"},
					address: domain.Address{ID: "address-1", Address: "Lenina st, 1"},
				},
				Products: benchProducts{products: products},
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
