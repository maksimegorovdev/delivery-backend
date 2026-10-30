//go:build integration

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"

	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/app"
)

// Фиксированные идентификаторы собственных фикстур.
const (
	smokeUserID    = "0190a000-0000-7000-8000-000000000001"
	smokeAddressID = "0190a000-0000-7000-8000-000000000011"
	smokeProductID = "0190a000-0000-7000-8000-0000000000a1"
	smokeOrderID   = "0190a000-0000-7000-8000-0000000000f1"
	smokeAddress   = "Lenina st, 1"
	smokePrice     = 5000
	smokeQuantity  = 2
)

const (
	eventuallyTimeout = 10 * time.Second
	eventuallyTick    = 50 * time.Millisecond
)

// fakeOrderServer возвращает заказ по запросу и запоминает последний запрос.
type fakeOrderServer struct {
	orderv1.UnimplementedOrderServiceServer

	got chan *orderv1.CreateOrderRequest
}

func (f *fakeOrderServer) CreateOrder(
	_ context.Context, req *orderv1.CreateOrderRequest,
) (*orderv1.CreateOrderResponse, error) {
	select {
	case f.got <- req:
	default:
	}

	items := make([]*orderv1.OrderItem, 0, len(req.GetItems()))
	for _, item := range req.GetItems() {
		items = append(items, &orderv1.OrderItem{
			ProductId:   item.GetProductId(),
			ProductName: "Bread",
			Quantity:    item.GetQuantity(),
			UnitPrice:   smokePrice,
		})
	}

	now := timestamppb.Now()

	return &orderv1.CreateOrderResponse{Order: &orderv1.Order{
		Id:              smokeOrderID,
		UserId:          req.GetUserId(),
		Status:          orderv1.OrderStatus_ORDER_STATUS_CREATED,
		Items:           items,
		TotalAmount:     smokePrice * smokeQuantity,
		DeliveryAddress: smokeAddress,
		CreatedAt:       now,
		UpdatedAt:       now,
	}}, nil
}

// startFakeOrder поднимает `order` на loopback-листенере с портом от ОС и возвращает его адрес.
func startFakeOrder(t *testing.T) (string, *fakeOrderServer) {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	fake := &fakeOrderServer{got: make(chan *orderv1.CreateOrderRequest, 1)}
	srv := grpc.NewServer()
	orderv1.RegisterOrderServiceServer(srv, fake)

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = srv.Serve(lis)
	}()

	t.Cleanup(func() {
		srv.Stop()
		<-serveDone
	})

	return lis.Addr().String(), fake
}

// setEnv задаёт окружение приложения; телеметрия выключена, адрес HTTP-сервера — httpAddr.
func setEnv(t *testing.T, httpAddr, orderAddr string) {
	t.Helper()

	t.Setenv("APP_NAME", "gateway")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("HTTP_ADDR", httpAddr)
	t.Setenv("ORDER_SERVICE_GRPC_ADDR", orderAddr)
	t.Setenv("FAKE_AUTH_USER_ID", smokeUserID)
	t.Setenv("OTEL_ENABLED", "false")
}

type orderResponse struct {
	Data struct {
		ID              string `json:"id"`
		UserID          string `json:"user_id"`
		Status          string `json:"status"`
		TotalAmount     int64  `json:"total_amount"`
		DeliveryAddress string `json:"delivery_address"`
		Items           []struct {
			ProductID string `json:"product_id"`
			Quantity  int32  `json:"quantity"`
			UnitPrice int64  `json:"unit_price"`
		} `json:"items"`
	} `json:"data"`
}

//nolint:paralleltest // t.Setenv
func TestApp_Smoke(t *testing.T) {
	orderAddr, fake := startFakeOrder(t)
	setEnv(t, "127.0.0.1:0", orderAddr)

	a, err := app.New(t.Context())
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	runDone := make(chan error, 1)
	go func() { runDone <- a.Run(runCtx) }()

	body, err := json.Marshal(map[string]any{
		"address_id": smokeAddressID,
		"items":      []map[string]any{{"product_id": smokeProductID, "quantity": smokeQuantity}},
	})
	require.NoError(t, err)

	url := "http://" + a.Addr().String() + "/api/v1/orders"

	var got orderResponse
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		req, reqErr := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(body))
		if !assert.NoError(c, reqErr) {
			return
		}
		req.Header.Set("Content-Type", "application/json")

		resp, doErr := http.DefaultClient.Do(req)
		if !assert.NoError(c, doErr) {
			return
		}
		defer func() { _ = resp.Body.Close() }()

		if assert.Equal(c, http.StatusCreated, resp.StatusCode) {
			assert.NoError(c, json.NewDecoder(resp.Body).Decode(&got))
		}
	}, eventuallyTimeout, eventuallyTick)

	assert.Equal(t, smokeOrderID, got.Data.ID)
	assert.Equal(t, smokeUserID, got.Data.UserID)
	assert.Equal(t, "created", got.Data.Status)
	assert.EqualValues(t, smokePrice*smokeQuantity, got.Data.TotalAmount)
	assert.Equal(t, smokeAddress, got.Data.DeliveryAddress)
	require.Len(t, got.Data.Items, 1)
	assert.Equal(t, smokeProductID, got.Data.Items[0].ProductID)
	assert.EqualValues(t, smokeQuantity, got.Data.Items[0].Quantity)

	// Gateway подставил пользователя из FakeAuth и передал тело запроса в `order`.
	forwarded := <-fake.got
	assert.Equal(t, smokeUserID, forwarded.GetUserId())
	assert.Equal(t, smokeAddressID, forwarded.GetAddressId())

	cancel()
	require.NoError(t, <-runDone)
	require.NoError(t, a.Close(t.Context()))
}

//nolint:paralleltest // t.Setenv
func TestApp_New_AddrInUse(t *testing.T) {
	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = busy.Close() })

	orderAddr, _ := startFakeOrder(t)
	setEnv(t, busy.Addr().String(), orderAddr)

	a, err := app.New(t.Context())
	require.Error(t, err)
	require.Nil(t, a)
}
