package client_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/grpctest"
	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"

	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/client"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/service"
)

// Времена различаются, чтобы перепутанные поля CreatedAt/UpdatedAt не прошли незамеченными.
func createdAt() time.Time { return time.Date(2025, time.March, 1, 10, 0, 0, 0, time.UTC) }

func updatedAt() time.Time { return time.Date(2025, time.March, 2, 11, 30, 0, 0, time.UTC) }

type fakeOrderServer struct {
	orderv1.UnimplementedOrderServiceServer

	got   *orderv1.CreateOrderRequest
	order *orderv1.Order
	err   error
}

func (f *fakeOrderServer) CreateOrder(
	_ context.Context,
	req *orderv1.CreateOrderRequest,
) (*orderv1.CreateOrderResponse, error) {
	f.got = req
	if f.err != nil {
		return nil, f.err
	}

	return &orderv1.CreateOrderResponse{Order: f.order}, nil
}

func newOrderClient(t *testing.T, srv *fakeOrderServer) *client.OrderClient {
	t.Helper()

	conn := grpctest.NewConn(t, func(s *grpc.Server) {
		orderv1.RegisterOrderServiceServer(s, srv)
	})

	return client.NewOrderClient(conn)
}

func TestOrderClient_CreateOrder(t *testing.T) {
	t.Parallel()

	input := service.CreateOrderInput{
		UserID:    "user-1",
		AddressID: "address-1",
		Items: []service.CreateOrderItemInput{
			{ProductID: "p-1", Quantity: 2},
			{ProductID: "p-2", Quantity: 1},
		},
	}

	t.Run("sends the request and maps all proto fields to domain", func(t *testing.T) {
		t.Parallel()

		srv := &fakeOrderServer{order: &orderv1.Order{
			Id:     "order-1",
			UserId: "user-1",
			Status: orderv1.OrderStatus_ORDER_STATUS_CREATED,
			Items: []*orderv1.OrderItem{
				{Id: "item-1", ProductId: "p-1", ProductName: "Milk", Quantity: 2, UnitPrice: 100},
				{Id: "item-2", ProductId: "p-2", ProductName: "Bread", Quantity: 1, UnitPrice: 50},
			},
			TotalAmount:     250,
			DeliveryAddress: "Lenina 1",
			CreatedAt:       timestamppb.New(createdAt()),
			UpdatedAt:       timestamppb.New(updatedAt()),
		}}
		c := newOrderClient(t, srv)

		got, err := c.CreateOrder(t.Context(), input)

		require.NoError(t, err)
		require.NotNil(t, srv.got)
		assert.Equal(t, "user-1", srv.got.GetUserId())
		assert.Equal(t, "address-1", srv.got.GetAddressId())
		require.Len(t, srv.got.GetItems(), 2)
		assert.Equal(t, "p-1", srv.got.GetItems()[0].GetProductId())
		assert.Equal(t, int32(2), srv.got.GetItems()[0].GetQuantity())
		assert.Equal(t, "p-2", srv.got.GetItems()[1].GetProductId())
		assert.Equal(t, int32(1), srv.got.GetItems()[1].GetQuantity())

		assert.Equal(t, domain.Order{
			ID:     "order-1",
			UserID: "user-1",
			Status: domain.OrderStatusCreated,
			Items: []domain.OrderItem{
				{ID: "item-1", ProductID: "p-1", ProductName: "Milk", Quantity: 2, UnitPrice: 100},
				{ID: "item-2", ProductID: "p-2", ProductName: "Bread", Quantity: 1, UnitPrice: 50},
			},
			TotalAmount:     250,
			DeliveryAddress: "Lenina 1",
			CreatedAt:       createdAt(),
			UpdatedAt:       updatedAt(),
		}, got)
	})

	t.Run("maps every proto status to its domain status", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			in   orderv1.OrderStatus
			want domain.OrderStatus
		}{
			{"created", orderv1.OrderStatus_ORDER_STATUS_CREATED, domain.OrderStatusCreated},
			{"paid", orderv1.OrderStatus_ORDER_STATUS_PAID, domain.OrderStatusPaid},
			{"confirmed", orderv1.OrderStatus_ORDER_STATUS_CONFIRMED, domain.OrderStatusConfirmed},
			{"assembling", orderv1.OrderStatus_ORDER_STATUS_ASSEMBLING, domain.OrderStatusAssembling},
			{"assembled", orderv1.OrderStatus_ORDER_STATUS_ASSEMBLED, domain.OrderStatusAssembled},
			{"courier assigned", orderv1.OrderStatus_ORDER_STATUS_COURIER_ASSIGNED, domain.OrderStatusCourierAssigned},
			{"delivering", orderv1.OrderStatus_ORDER_STATUS_DELIVERING, domain.OrderStatusDelivering},
			{"delivered", orderv1.OrderStatus_ORDER_STATUS_DELIVERED, domain.OrderStatusDelivered},
			{"canceled", orderv1.OrderStatus_ORDER_STATUS_CANCELED, domain.OrderStatusCanceled},
			{"unspecified", orderv1.OrderStatus_ORDER_STATUS_UNSPECIFIED, domain.OrderStatusUnspecified},
			{"unknown value", orderv1.OrderStatus(999), domain.OrderStatusUnspecified},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				c := newOrderClient(t, &fakeOrderServer{order: &orderv1.Order{Id: "order-1", Status: tt.in}})

				got, err := c.CreateOrder(t.Context(), input)

				require.NoError(t, err)
				assert.Equal(t, tt.want, got.Status)
			})
		}
	})

	t.Run("sends an empty items list and returns empty non-nil items", func(t *testing.T) {
		t.Parallel()

		srv := &fakeOrderServer{order: &orderv1.Order{Id: "order-1"}}
		c := newOrderClient(t, srv)

		got, err := c.CreateOrder(t.Context(), service.CreateOrderInput{UserID: "user-1"})

		require.NoError(t, err)
		assert.Empty(t, srv.got.GetItems())
		assert.NotNil(t, got.Items)
		assert.Empty(t, got.Items)
	})

	t.Run("maps grpc error codes to apperr codes", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			code codes.Code
			want apperr.Code
		}{
			{"not found", codes.NotFound, apperr.CodeNotFound},
			{"invalid argument", codes.InvalidArgument, apperr.CodeInvalidArgument},
			{"internal", codes.Internal, apperr.CodeInternal},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				c := newOrderClient(t, &fakeOrderServer{err: status.Error(tt.code, "boom")})

				got, err := c.CreateOrder(t.Context(), input)

				appErr, ok := apperr.As(err)
				require.True(t, ok, "want apperr.Error in chain, got %T", err)
				assert.Equal(t, tt.want, appErr.Code)
				assert.Equal(t, domain.Order{}, got)
			})
		}
	})
}
