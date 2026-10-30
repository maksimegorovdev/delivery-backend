//go:build integration

package grpc_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"

	"buf.build/go/protovalidate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/grpctest"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"
	addressv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/address/v1"
	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"
	productv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/product/v1"
	userv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/user/v1"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/client"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/event"
	repo "github.com/maksimegorovdev/delivery-backend/services/order/internal/repository/pg"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/service"
	grpctransport "github.com/maksimegorovdev/delivery-backend/services/order/internal/transport/grpc"
)

const migrationsDir = "../../../migrations/pg"

// Фиксированные идентификаторы собственных фикстур: на seed-миграции тесты не опираются.
const (
	stackUserID    = "0190a000-0000-7000-8000-000000000001"
	stackAddressID = "0190a000-0000-7000-8000-000000000011"
	stackBreadID   = "0190a000-0000-7000-8000-0000000000a1"
	stackMilkID    = "0190a000-0000-7000-8000-0000000000a2"
	stackAddress   = "Lenina st, 1"
	stackBreadName = "Bread"
	stackMilkName  = "Milk"
	stackBreadCost = 5000
	stackMilkCost  = 9000
)

// fakeDeps — поведение фейковых зависимостей `order`: `user` и `product`.
type fakeDeps struct {
	userMissing    bool
	addressMissing bool
	productsError  error // ошибка, которую вернёт `product`; приоритетнее каталога
	catalog        map[string]*productv1.Product

	calls atomic.Int32 // число обращений ко всем фейкам
}

func defaultCatalog() map[string]*productv1.Product {
	return map[string]*productv1.Product{
		stackBreadID: {Id: stackBreadID, Name: stackBreadName, Price: stackBreadCost},
		stackMilkID:  {Id: stackMilkID, Name: stackMilkName, Price: stackMilkCost},
	}
}

type fakeUserServer struct {
	userv1.UnimplementedUserServiceServer

	deps *fakeDeps
}

func (s *fakeUserServer) GetUser(_ context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	s.deps.calls.Add(1)
	if s.deps.userMissing {
		return nil, status.Error(codes.NotFound, "user not found")
	}

	return &userv1.GetUserResponse{User: &userv1.User{Id: req.GetId()}}, nil
}

type fakeAddressServer struct {
	addressv1.UnimplementedAddressServiceServer

	deps *fakeDeps
}

func (s *fakeAddressServer) GetAddress(
	_ context.Context, req *addressv1.GetAddressRequest,
) (*addressv1.GetAddressResponse, error) {
	s.deps.calls.Add(1)
	if s.deps.addressMissing {
		return nil, status.Error(codes.NotFound, "address not found")
	}

	return &addressv1.GetAddressResponse{Address: &addressv1.Address{
		Id: req.GetId(), UserId: stackUserID, Address: stackAddress,
	}}, nil
}

type fakeProductServer struct {
	productv1.UnimplementedProductServiceServer

	deps *fakeDeps
}

func (s *fakeProductServer) GetProducts(
	_ context.Context, req *productv1.GetProductsRequest,
) (*productv1.GetProductsResponse, error) {
	s.deps.calls.Add(1)
	if s.deps.productsError != nil {
		return nil, s.deps.productsError
	}

	// Отсутствующие в каталоге товары просто не попадают в ответ.
	var found []*productv1.Product
	for _, id := range req.GetIds() {
		if p, ok := s.deps.catalog[id]; ok {
			found = append(found, p)
		}
	}

	return &productv1.GetProductsResponse{Products: found}, nil
}

// newOrderStack собирает боевой стек `order` так же, как app: интерцепторы Error → Logger → Validation,
// реальный OrderService, репозитории и TxManager на pgtest. `user` и `product` — фейковые gRPC-серверы.
func newOrderStack(t *testing.T, deps *fakeDeps) (orderv1.OrderServiceClient, *pgxpool.Pool) {
	t.Helper()

	pool := pgtest.NewDB(t, migrationsDir)

	depsConn := grpctest.NewConn(t, func(s *grpc.Server) {
		userv1.RegisterUserServiceServer(s, &fakeUserServer{deps: deps})
		addressv1.RegisterAddressServiceServer(s, &fakeAddressServer{deps: deps})
		productv1.RegisterProductServiceServer(s, &fakeProductServer{deps: deps})
	})

	svc := service.NewOrderService(service.OrderServiceDeps{
		Tx:       postgres.NewTxManager(pool),
		Orders:   repo.NewOrderRepo(pool),
		Outbox:   repo.NewOutboxRepo(pool),
		Events:   event.NewOrderEventBuilder(),
		Users:    client.NewUserClient(depsConn),
		Products: client.NewProductClient(depsConn),
	})

	validator, err := protovalidate.New()
	require.NoError(t, err)

	orderConn := grpctest.NewConn(t,
		func(s *grpc.Server) {
			grpctransport.NewRouter(grpctransport.RouterDeps{Server: s, OrderService: svc})
		},
		grpc.ChainUnaryInterceptor(
			interceptors.Error(),
			interceptors.Logger(slog.New(slog.DiscardHandler)),
			interceptors.Validation(validator),
		),
	)

	return orderv1.NewOrderServiceClient(orderConn), pool
}

func stackRequest() *orderv1.CreateOrderRequest {
	return &orderv1.CreateOrderRequest{
		UserId:    stackUserID,
		AddressId: stackAddressID,
		Items: []*orderv1.CreateOrderItem{
			{ProductId: stackBreadID, Quantity: 2},
			{ProductId: stackMilkID, Quantity: 1},
		},
	}
}

// stackRowCounts — число строк в таблицах заказа и outbox.
type stackRowCounts struct {
	orders, items, events int
}

func countStackRows(t *testing.T, pool *pgxpool.Pool) stackRowCounts {
	t.Helper()

	var got stackRowCounts
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM orders`).Scan(&got.orders))
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM order_items`).Scan(&got.items))
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox_events`).Scan(&got.events))

	return got
}
