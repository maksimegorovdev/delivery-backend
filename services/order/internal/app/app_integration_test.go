//go:build integration

package app_test

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"
	addressv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/address/v1"
	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"
	productv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/product/v1"
	userv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/user/v1"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/app"
)

const migrationsDir = "../../migrations/pg"

// appName помечает соединения приложения в pg_stat_activity, чтобы отличать их от соединений пула pgtest.
const appName = "order-app-test"

// Фиксированные идентификаторы собственных фикстур: на seed-миграции тесты не опираются.
const (
	smokeUserID    = "0190a000-0000-7000-8000-000000000001"
	smokeAddressID = "0190a000-0000-7000-8000-000000000011"
	smokeProductID = "0190a000-0000-7000-8000-0000000000a1"
	smokeAddress   = "Lenina st, 1"
	smokePrice     = 5000
)

const (
	eventuallyTimeout = 10 * time.Second
	eventuallyTick    = 50 * time.Millisecond
)

type fakeUserServer struct {
	userv1.UnimplementedUserServiceServer
}

func (fakeUserServer) GetUser(_ context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	return &userv1.GetUserResponse{User: &userv1.User{Id: req.GetId()}}, nil
}

type fakeAddressServer struct {
	addressv1.UnimplementedAddressServiceServer
}

func (fakeAddressServer) GetAddress(
	_ context.Context, req *addressv1.GetAddressRequest,
) (*addressv1.GetAddressResponse, error) {
	return &addressv1.GetAddressResponse{Address: &addressv1.Address{
		Id: req.GetId(), UserId: smokeUserID, Address: smokeAddress,
	}}, nil
}

type fakeProductServer struct {
	productv1.UnimplementedProductServiceServer
}

func (fakeProductServer) GetProducts(
	_ context.Context, req *productv1.GetProductsRequest,
) (*productv1.GetProductsResponse, error) {
	products := make([]*productv1.Product, 0, len(req.GetIds()))
	for _, id := range req.GetIds() {
		products = append(products, &productv1.Product{Id: id, Name: "Bread", Price: smokePrice})
	}

	return &productv1.GetProductsResponse{Products: products}, nil
}

// startFakeDeps поднимает `user` и `product` на loopback-листенере с портом от ОС и возвращает его адрес.
func startFakeDeps(t *testing.T) string {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	userv1.RegisterUserServiceServer(srv, fakeUserServer{})
	addressv1.RegisterAddressServiceServer(srv, fakeAddressServer{})
	productv1.RegisterProductServiceServer(srv, fakeProductServer{})

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = srv.Serve(lis)
	}()

	t.Cleanup(func() {
		srv.Stop()
		<-serveDone
	})

	return lis.Addr().String()
}

// dsnOf собирает DSN к той же БД, что и пул pgtest: приложению нужна строка подключения, а не пул.
func dsnOf(pool *pgxpool.Pool) string {
	cc := pool.Config().ConnConfig

	u := url.URL{
		Scheme:   "postgresql",
		User:     url.UserPassword(cc.User, cc.Password),
		Host:     net.JoinHostPort(cc.Host, strconv.Itoa(int(cc.Port))),
		Path:     "/" + cc.Database,
		RawQuery: url.Values{"sslmode": {"disable"}, "application_name": {appName}}.Encode(),
	}

	return u.String()
}

// setEnv задаёт окружение приложения; телеметрия выключена, адрес gRPC-сервера — grpcAddr.
func setEnv(t *testing.T, dsn, grpcAddr, depsAddr string) {
	t.Helper()

	t.Setenv("APP_NAME", "order")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("PG_DSN", dsn)
	t.Setenv("PG_MAX_CONNS", "4")
	t.Setenv("GRPC_SERVER_ADDR", grpcAddr)
	t.Setenv("GRPC_SERVER_REFLECTION", "true")
	t.Setenv("USER_SERVICE_GRPC_ADDR", depsAddr)
	t.Setenv("PRODUCT_SERVICE_GRPC_ADDR", depsAddr)
	t.Setenv("OTEL_ENABLED", "false")
}

//nolint:paralleltest // t.Setenv
func TestApp_Smoke(t *testing.T) {
	pool := pgtest.NewDB(t, migrationsDir)
	setEnv(t, dsnOf(pool), "127.0.0.1:0", startFakeDeps(t))

	a, err := app.New(t.Context())
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(runCtx) }()

	conn, err := grpc.NewClient(a.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	var resp *orderv1.CreateOrderResponse
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var callErr error
		resp, callErr = orderv1.NewOrderServiceClient(conn).CreateOrder(t.Context(), &orderv1.CreateOrderRequest{
			UserId:    smokeUserID,
			AddressId: smokeAddressID,
			Items:     []*orderv1.CreateOrderItem{{ProductId: smokeProductID, Quantity: 2}},
		})
		assert.NoError(c, callErr)
	}, eventuallyTimeout, eventuallyTick)
	require.NotEmpty(t, resp.GetOrder().GetId())

	cancel()
	require.NoError(t, <-runDone)
	require.NoError(t, a.Close(t.Context()))
}

//nolint:paralleltest // t.Setenv
func TestApp_New_AddrInUse(t *testing.T) {
	pool := pgtest.NewDB(t, migrationsDir)

	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = busy.Close() })

	setEnv(t, dsnOf(pool), busy.Addr().String(), startFakeDeps(t))

	a, err := app.New(t.Context())
	require.Error(t, err)
	require.Nil(t, a)

	// Ресурсы освобождены: пул приложения закрыт, его соединений в БД не осталось.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var conns int
		queryErr := pool.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity WHERE application_name = $1`, appName,
		).Scan(&conns)
		assert.NoError(c, queryErr)
		assert.Zero(c, conns)
	}, eventuallyTimeout, eventuallyTick)
}
