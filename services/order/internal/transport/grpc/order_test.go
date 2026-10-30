package grpc_test

import (
	"errors"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/grpctest"
	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/service"
	grpctransport "github.com/maksimegorovdev/delivery-backend/services/order/internal/transport/grpc"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/transport/grpc/mocks"
)

const (
	userID     = "0197a1b2-0000-7000-8000-000000000001"
	addressID  = "0197a1b2-0000-7000-8000-000000000002"
	productID1 = "0197a1b2-0000-7000-8000-000000000003"
	productID2 = "0197a1b2-0000-7000-8000-000000000004"
)

// newOrderClient поднимает роутер с теми же интерцепторами и в том же порядке, что и app сервиса.
func newOrderClient(t *testing.T, svc grpctransport.OrderService) orderv1.OrderServiceClient {
	t.Helper()

	validator, err := protovalidate.New()
	require.NoError(t, err)

	conn := grpctest.NewConn(t,
		func(s *grpc.Server) {
			grpctransport.NewRouter(grpctransport.RouterDeps{Server: s, OrderService: svc})
		},
		grpc.ChainUnaryInterceptor(
			interceptors.Error(),
			interceptors.Validation(validator),
		),
	)

	return orderv1.NewOrderServiceClient(conn)
}

func validRequest() *orderv1.CreateOrderRequest {
	return &orderv1.CreateOrderRequest{
		UserId:    userID,
		AddressId: addressID,
		Items: []*orderv1.CreateOrderItem{
			{ProductId: productID1, Quantity: 2},
			{ProductId: productID2, Quantity: 1},
		},
	}
}

// Времена различаются, чтобы перепутанные CreatedAt/UpdatedAt не прошли незамеченными.
func domainOrder(status domain.OrderStatus) domain.Order {
	return domain.Order{
		ID:     "order-1",
		UserID: userID,
		Status: status,
		Items: []domain.OrderItem{
			{ID: "item-1", OrderID: "order-1", ProductID: productID1, ProductName: "Milk", Quantity: 2, UnitPrice: 150},
			{ID: "item-2", OrderID: "order-1", ProductID: productID2, ProductName: "Bread", Quantity: 1, UnitPrice: 80},
		},
		TotalAmount:     380,
		DeliveryAddress: "Lenina 1, 10",
		CreatedAt:       time.Date(2025, time.March, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2025, time.March, 2, 11, 30, 0, 0, time.UTC),
	}
}

func TestOrderRouter_CreateOrder_Success(t *testing.T) {
	t.Parallel()

	svc := mocks.NewMockOrderService(t)
	wantInput := service.CreateOrderInput{
		UserID:    userID,
		AddressID: addressID,
		Items: []service.CreateOrderItemInput{
			{ProductID: productID1, Quantity: 2},
			{ProductID: productID2, Quantity: 1},
		},
	}
	order := domainOrder(domain.OrderStatusCreated)
	svc.EXPECT().CreateOrder(mock.Anything, wantInput).Return(order, nil)
	client := newOrderClient(t, svc)

	resp, err := client.CreateOrder(t.Context(), validRequest())

	require.NoError(t, err)
	got := resp.GetOrder()
	assert.Equal(t, order.ID, got.GetId())
	assert.Equal(t, order.UserID, got.GetUserId())
	assert.Equal(t, orderv1.OrderStatus_ORDER_STATUS_CREATED, got.GetStatus())
	assert.Equal(t, order.TotalAmount, got.GetTotalAmount())
	assert.Equal(t, order.DeliveryAddress, got.GetDeliveryAddress())
	assert.True(t, order.CreatedAt.Equal(got.GetCreatedAt().AsTime()))
	assert.True(t, order.UpdatedAt.Equal(got.GetUpdatedAt().AsTime()))

	require.Len(t, got.GetItems(), len(order.Items))
	for i, want := range order.Items {
		item := got.GetItems()[i]
		assert.Equal(t, want.ID, item.GetId())
		assert.Equal(t, want.ProductID, item.GetProductId())
		assert.Equal(t, want.ProductName, item.GetProductName())
		assert.Equal(t, want.Quantity, item.GetQuantity())
		assert.Equal(t, want.UnitPrice, item.GetUnitPrice())
	}
}

func TestOrderRouter_CreateOrder_StatusMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status domain.OrderStatus
		want   orderv1.OrderStatus
	}{
		{name: "created", status: domain.OrderStatusCreated, want: orderv1.OrderStatus_ORDER_STATUS_CREATED},
		{name: "paid", status: domain.OrderStatusPaid, want: orderv1.OrderStatus_ORDER_STATUS_PAID},
		{name: "confirmed", status: domain.OrderStatusConfirmed, want: orderv1.OrderStatus_ORDER_STATUS_CONFIRMED},
		{name: "assembling", status: domain.OrderStatusAssembling, want: orderv1.OrderStatus_ORDER_STATUS_ASSEMBLING},
		{name: "assembled", status: domain.OrderStatusAssembled, want: orderv1.OrderStatus_ORDER_STATUS_ASSEMBLED},
		{
			name:   "courier assigned",
			status: domain.OrderStatusCourierAssigned,
			want:   orderv1.OrderStatus_ORDER_STATUS_COURIER_ASSIGNED,
		},
		{name: "delivering", status: domain.OrderStatusDelivering, want: orderv1.OrderStatus_ORDER_STATUS_DELIVERING},
		{name: "delivered", status: domain.OrderStatusDelivered, want: orderv1.OrderStatus_ORDER_STATUS_DELIVERED},
		{name: "canceled", status: domain.OrderStatusCanceled, want: orderv1.OrderStatus_ORDER_STATUS_CANCELED},
		{
			name:   "unknown falls back to unspecified",
			status: "bogus",
			want:   orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED,
		},
		{name: "empty falls back to unspecified", status: "", want: orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := mocks.NewMockOrderService(t)
			svc.EXPECT().CreateOrder(mock.Anything, mock.Anything).Return(domainOrder(tt.status), nil)
			client := newOrderClient(t, svc)

			resp, err := client.CreateOrder(t.Context(), validRequest())

			require.NoError(t, err)
			assert.Equal(t, tt.want, resp.GetOrder().GetStatus())
		})
	}
}

func TestOrderRouter_CreateOrder_ServiceError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{name: "not found", err: apperr.NotFound(), wantCode: codes.NotFound},
		{name: "invalid argument", err: apperr.InvalidArgument(), wantCode: codes.InvalidArgument},
		{name: "failed precondition", err: apperr.FailedPrecondition(), wantCode: codes.FailedPrecondition},
		{name: "unavailable", err: apperr.Unavailable(), wantCode: codes.Unavailable},
		{
			name:     "wrapped apperr keeps its code",
			err:      apperr.NotFound().Wrap(errors.New("db")),
			wantCode: codes.NotFound,
		},
		{name: "plain error becomes internal", err: errors.New("boom"), wantCode: codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := mocks.NewMockOrderService(t)
			svc.EXPECT().CreateOrder(mock.Anything, mock.Anything).Return(domain.Order{}, tt.err)
			client := newOrderClient(t, svc)

			_, err := client.CreateOrder(t.Context(), validRequest())

			require.Error(t, err)
			assert.Equal(t, tt.wantCode, status.Code(err))
		})
	}
}

func TestOrderRouter_CreateOrder_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(req *orderv1.CreateOrderRequest)
	}{
		{name: "user id is not a uuid", mutate: func(r *orderv1.CreateOrderRequest) { r.UserId = "not-a-uuid" }},
		{name: "user id is empty", mutate: func(r *orderv1.CreateOrderRequest) { r.UserId = "" }},
		{name: "address id is not a uuid", mutate: func(r *orderv1.CreateOrderRequest) { r.AddressId = "not-a-uuid" }},
		{name: "items are empty", mutate: func(r *orderv1.CreateOrderRequest) { r.Items = nil }},
		{name: "product id is not a uuid", mutate: func(r *orderv1.CreateOrderRequest) { r.Items[0].ProductId = "x" }},
		{name: "quantity is zero", mutate: func(r *orderv1.CreateOrderRequest) { r.Items[0].Quantity = 0 }},
		{name: "quantity is negative", mutate: func(r *orderv1.CreateOrderRequest) { r.Items[1].Quantity = -1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Мок без ожиданий: любой вызов сервиса провалит тест.
			svc := mocks.NewMockOrderService(t)
			client := newOrderClient(t, svc)
			req := validRequest()
			tt.mutate(req)

			_, err := client.CreateOrder(t.Context(), req)

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
			svc.AssertNotCalled(t, "CreateOrder", mock.Anything, mock.Anything)
		})
	}
}
