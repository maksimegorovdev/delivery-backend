package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/service/mocks"
)

// fakeTx выполняет fn сразу и отмечает, что код находится внутри транзакции.
type fakeTx struct {
	isInTx bool
}

func (f *fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	f.isInTx = true
	defer func() { f.isInTx = false }()

	return fn(ctx)
}

type deps struct {
	tx       *fakeTx
	orders   *mocks.MockOrderRepo
	outbox   *mocks.MockOutboxRepo
	events   *mocks.MockOrderEvents
	users    *mocks.MockUserProvider
	products *mocks.MockProductProvider
}

func newDeps(t *testing.T) deps {
	t.Helper()

	return deps{
		tx:       &fakeTx{},
		orders:   mocks.NewMockOrderRepo(t),
		outbox:   mocks.NewMockOutboxRepo(t),
		events:   mocks.NewMockOrderEvents(t),
		users:    mocks.NewMockUserProvider(t),
		products: mocks.NewMockProductProvider(t),
	}
}

func (d deps) service() *service.OrderService {
	return service.NewOrderService(service.OrderServiceDeps{
		Tx:       d.tx,
		Orders:   d.orders,
		Outbox:   d.outbox,
		Events:   d.events,
		Users:    d.users,
		Products: d.products,
	})
}

func fixtureInput() service.CreateOrderInput {
	return service.CreateOrderInput{
		UserID:    "user-1",
		AddressID: "address-1",
		Items: []service.CreateOrderItemInput{
			{ProductID: "product-1", Quantity: 2},
			{ProductID: "product-2", Quantity: 1},
		},
	}
}

func fixtureProducts() []domain.Product {
	return []domain.Product{
		{ID: "product-1", Name: "Milk", Price: 100},
		{ID: "product-2", Name: "Bread", Price: 50},
	}
}

func TestOrderService_CreateOrder(t *testing.T) {
	t.Parallel()

	errUnavailable := errors.New("user service unavailable")

	tests := []struct {
		name    string
		setup   func(d deps)
		wantErr error
	}{
		{
			name: "propagates user service error without saving order",
			setup: func(d deps) {
				d.users.EXPECT().GetUser(mock.Anything, "user-1").Return(domain.User{}, errUnavailable)
			},
			wantErr: errUnavailable,
		},
		{
			name: "returns product not found when catalog misses an item",
			setup: func(d deps) {
				d.users.EXPECT().GetUser(mock.Anything, "user-1").Return(domain.User{ID: "user-1"}, nil)
				d.users.EXPECT().GetAddress(mock.Anything, "address-1").
					Return(domain.Address{ID: "address-1", Address: "Main st, 1"}, nil)
				d.products.EXPECT().GetProducts(mock.Anything, []string{"product-1", "product-2"}).
					Return(fixtureProducts()[:1], nil)
			},
			wantErr: domain.ErrProductNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			must := require.New(t)

			d := newDeps(t)
			tt.setup(d)

			_, err := d.service().CreateOrder(t.Context(), fixtureInput())

			must.ErrorIs(err, tt.wantErr)
		})
	}
}

func TestOrderService_CreateOrder_Success(t *testing.T) {
	t.Parallel()

	is := assert.New(t)
	must := require.New(t)

	d := newDeps(t)
	event := domain.OutboxEvent{ID: "event-1", Type: domain.EventTypeOrderCreated}

	var created domain.Order

	d.users.EXPECT().GetUser(mock.Anything, "user-1").Return(domain.User{ID: "user-1"}, nil)
	d.users.EXPECT().GetAddress(mock.Anything, "address-1").
		Return(domain.Address{ID: "address-1", Address: "Main st, 1"}, nil)
	d.products.EXPECT().GetProducts(mock.Anything, []string{"product-1", "product-2"}).
		Return(fixtureProducts(), nil)
	d.events.EXPECT().OrderCreated(mock.Anything).Return(event, nil)
	d.orders.EXPECT().Create(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, order domain.Order) error {
			is.True(d.tx.isInTx, "Create must be called inside transaction")
			created = order

			return nil
		})
	d.outbox.EXPECT().Save(mock.Anything, event).
		RunAndReturn(func(_ context.Context, _ domain.OutboxEvent) error {
			is.True(d.tx.isInTx, "Save must be called inside transaction")

			return nil
		})

	got, err := d.service().CreateOrder(t.Context(), fixtureInput())

	must.NoError(err)
	is.Equal(created, got)
	is.Equal("user-1", got.UserID)
	is.Equal("Main st, 1", got.DeliveryAddress)
	is.Equal(domain.OrderStatusCreated, got.Status)
	is.Equal(int64(250), got.TotalAmount)
	must.Len(got.Items, 2)
	is.Equal("Milk", got.Items[0].ProductName)
	is.Equal(int32(2), got.Items[0].Quantity)
	is.Equal(int64(100), got.Items[0].UnitPrice)
	is.Equal("Bread", got.Items[1].ProductName)
	is.Equal(int64(50), got.Items[1].UnitPrice)
}
