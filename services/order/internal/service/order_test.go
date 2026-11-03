package service_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/service/mocks"
)

// fakeTx выполняет fn сразу и отмечает, что код находится внутри транзакции.
// Если задан err, транзакция не открывается и fn не вызывается.
type fakeTx struct {
	isInTx bool
	err    error
}

func (f *fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if f.err != nil {
		return f.err
	}

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

// expectUserResolved настраивает успешное получение пользователя и адреса доставки.
func expectUserResolved(d deps) {
	d.users.EXPECT().GetUser(mock.Anything, "user-1").Return(domain.User{ID: "user-1"}, nil)
	expectAddressResolved(d)
}

// expectAddressResolved настраивает успешное получение адреса доставки.
func expectAddressResolved(d deps) {
	d.users.EXPECT().GetAddress(mock.Anything, "address-1").
		Return(domain.Address{ID: "address-1", Address: "Main st, 1"}, nil)
}

// expectProductsLoaded настраивает успешную загрузку каталога для fixtureInput.
func expectProductsLoaded(d deps) {
	d.products.EXPECT().GetProducts(mock.Anything, []string{"product-1", "product-2"}).
		Return(fixtureProducts(), nil)
}

// errNotConcurrent — участник barrier не дождался остальных: вызовы идут не одновременно.
var errNotConcurrent = errors.New("lookups are not concurrent")

// barrier пропускает участников, только когда до него дошли все n. При последовательных
// вызовах первый участник ждёт в одиночку и через секунду получает errNotConcurrent.
type barrier struct {
	n       int32
	arrived atomic.Int32
	all     chan struct{}
}

func newBarrier(n int32) *barrier {
	return &barrier{n: n, all: make(chan struct{})}
}

func (b *barrier) wait() error {
	if b.arrived.Add(1) == b.n {
		close(b.all)
	}

	select {
	case <-b.all:
		return nil
	case <-time.After(time.Second):
		return errNotConcurrent
	}
}

// waitCanceled блокируется до отмены ctx — имитирует зависимость, которая отвечает, только если её отменить.
func waitCanceled(ctx context.Context) error {
	<-ctx.Done()

	return ctx.Err()
}

// expectOrderPrepared настраивает успешные вызовы до транзакции: пользователь, адрес, каталог, событие.
func expectOrderPrepared(d deps) domain.OutboxEvent {
	event := domain.OutboxEvent{ID: "event-1", Type: domain.EventTypeOrderCreated}

	expectUserResolved(d)
	d.products.EXPECT().GetProducts(mock.Anything, []string{"product-1", "product-2"}).
		Return(fixtureProducts(), nil)
	d.events.EXPECT().OrderCreated(mock.Anything).Return(event, nil)

	return event
}

func TestOrderService_CreateOrder(t *testing.T) {
	t.Parallel()

	errUnavailable := errors.New("user service unavailable")
	errDB := errors.New("db is down")
	errEvent := errors.New("marshal failed")

	tests := []struct {
		name    string
		setup   func(d deps)
		wantErr []error
	}{
		{
			// Пользователь, адрес и каталог запрашиваются параллельно, поэтому адрес и каталог
			// вызываются и при ошибке user; Create и Save без EXPECT — заказ не сохраняется.
			name: "propagates user service error without saving order",
			setup: func(d deps) {
				d.users.EXPECT().GetUser(mock.Anything, "user-1").Return(domain.User{}, errUnavailable)
				expectAddressResolved(d)
				expectProductsLoaded(d)
			},
			wantErr: []error{errUnavailable},
		},
		{
			name: "propagates address lookup error",
			setup: func(d deps) {
				d.users.EXPECT().GetUser(mock.Anything, "user-1").Return(domain.User{ID: "user-1"}, nil)
				d.users.EXPECT().GetAddress(mock.Anything, "address-1").Return(domain.Address{}, errUnavailable)
				expectProductsLoaded(d)
			},
			wantErr: []error{errUnavailable},
		},
		{
			name: "propagates product catalog error",
			setup: func(d deps) {
				expectUserResolved(d)
				d.products.EXPECT().GetProducts(mock.Anything, []string{"product-1", "product-2"}).
					Return(nil, errUnavailable)
			},
			wantErr: []error{errUnavailable},
		},
		{
			name: "returns product not found when catalog misses an item",
			setup: func(d deps) {
				expectUserResolved(d)
				d.products.EXPECT().GetProducts(mock.Anything, []string{"product-1", "product-2"}).
					Return(fixtureProducts()[:1], nil)
			},
			wantErr: []error{domain.ErrProductNotFound, apperr.NotFound()},
		},
		{
			name: "propagates event builder error without opening transaction",
			setup: func(d deps) {
				expectUserResolved(d)
				d.products.EXPECT().GetProducts(mock.Anything, []string{"product-1", "product-2"}).
					Return(fixtureProducts(), nil)
				d.events.EXPECT().OrderCreated(mock.Anything).Return(domain.OutboxEvent{}, errEvent)
			},
			wantErr: []error{errEvent},
		},
		{
			// Create и Save без EXPECT: строгие моки упадут, если репозитории вызовутся при отказе транзакции.
			name: "propagates transaction error without touching repos",
			setup: func(d deps) {
				expectOrderPrepared(d)
				d.tx.err = errDB
			},
			wantErr: []error{errDB},
		},
		{
			// Save без EXPECT: строгий мок упадёт, если outbox вызовется после ошибки Create.
			name: "propagates order repo error without saving outbox event",
			setup: func(d deps) {
				expectOrderPrepared(d)
				d.orders.EXPECT().Create(mock.Anything, mock.Anything).Return(errDB)
			},
			wantErr: []error{errDB},
		},
		{
			name: "propagates outbox error",
			setup: func(d deps) {
				event := expectOrderPrepared(d)
				d.orders.EXPECT().Create(mock.Anything, mock.Anything).Return(nil)
				d.outbox.EXPECT().Save(mock.Anything, event).Return(errDB)
			},
			wantErr: []error{errDB},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			must := require.New(t)

			d := newDeps(t)
			tt.setup(d)

			_, err := d.service().CreateOrder(t.Context(), fixtureInput())

			for _, want := range tt.wantErr {
				must.ErrorIs(err, want)
			}
		})
	}
}

func TestOrderService_CreateOrder_Success(t *testing.T) {
	t.Parallel()

	is := assert.New(t)
	must := require.New(t)

	d := newDeps(t)
	event := expectOrderPrepared(d)

	var created domain.Order

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

func TestOrderService_CreateOrder_LookupsRunConcurrently(t *testing.T) {
	t.Parallel()

	// synctest: при последовательном коде barrier ждёт по фиктивным часам, и тест падает сразу.
	synctest.Test(t, func(t *testing.T) {
		d := newDeps(t)
		lookups := newBarrier(3)

		d.users.EXPECT().GetUser(mock.Anything, "user-1").
			RunAndReturn(func(context.Context, string) (domain.User, error) {
				return domain.User{ID: "user-1"}, lookups.wait()
			})
		d.users.EXPECT().GetAddress(mock.Anything, "address-1").
			RunAndReturn(func(context.Context, string) (domain.Address, error) {
				return domain.Address{ID: "address-1", Address: "Main st, 1"}, lookups.wait()
			})
		d.products.EXPECT().GetProducts(mock.Anything, []string{"product-1", "product-2"}).
			RunAndReturn(func(context.Context, []string) ([]domain.Product, error) {
				return fixtureProducts(), lookups.wait()
			})
		d.events.EXPECT().OrderCreated(mock.Anything).Return(domain.OutboxEvent{ID: "event-1"}, nil)
		d.orders.EXPECT().Create(mock.Anything, mock.Anything).Return(nil)
		d.outbox.EXPECT().Save(mock.Anything, mock.Anything).Return(nil)

		got, err := d.service().CreateOrder(t.Context(), fixtureInput())

		require.NoError(t, err)
		assert.Equal(t, "Main st, 1", got.DeliveryAddress)
		assert.Len(t, got.Items, 2)
	})
}

func TestOrderService_CreateOrder_FirstLookupErrorCancelsOthers(t *testing.T) {
	t.Parallel()

	// synctest: если отмены нет, все горутины зависнут, и synctest сообщит о deadlock вместо таймаута теста.
	synctest.Test(t, func(t *testing.T) {
		errUnavailable := errors.New("user service unavailable")
		d := newDeps(t)

		d.users.EXPECT().GetUser(mock.Anything, "user-1").Return(domain.User{}, errUnavailable)
		d.users.EXPECT().GetAddress(mock.Anything, "address-1").
			RunAndReturn(func(ctx context.Context, _ string) (domain.Address, error) {
				return domain.Address{}, waitCanceled(ctx)
			})
		d.products.EXPECT().GetProducts(mock.Anything, []string{"product-1", "product-2"}).
			RunAndReturn(func(ctx context.Context, _ []string) ([]domain.Product, error) {
				return nil, waitCanceled(ctx)
			})

		_, err := d.service().CreateOrder(t.Context(), fixtureInput())

		// Возвращается исходная ошибка, а не context.Canceled отменённых соседей.
		require.ErrorIs(t, err, errUnavailable)
		require.NotErrorIs(t, err, context.Canceled)
	})
}

func TestOrderService_CreateOrder_EmptyItems(t *testing.T) {
	t.Parallel()

	must := require.New(t)

	d := newDeps(t)
	expectUserResolved(d)
	d.products.EXPECT().GetProducts(mock.Anything, []string{}).Return(nil, nil)

	input := fixtureInput()
	input.Items = nil

	_, err := d.service().CreateOrder(t.Context(), input)

	must.ErrorIs(err, domain.ErrOrderNoItems)
	must.ErrorIs(err, apperr.InvalidArgument())
}
