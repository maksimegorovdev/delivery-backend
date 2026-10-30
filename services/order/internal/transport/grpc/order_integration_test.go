//go:build integration

package grpc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"
	productv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/product/v1"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

func TestOrderStack_CreateOrder(t *testing.T) {
	t.Parallel()

	t.Run("persists order, items and OrderCreated event", func(t *testing.T) {
		t.Parallel()

		deps := &fakeDeps{catalog: defaultCatalog()}
		cli, pool := newOrderStack(t, deps)

		resp, err := cli.CreateOrder(t.Context(), stackRequest())

		require.NoError(t, err)
		order := resp.GetOrder()
		assert.Equal(t, stackUserID, order.GetUserId())
		assert.Equal(t, stackAddress, order.GetDeliveryAddress())
		assert.Equal(t, orderv1.OrderStatus_ORDER_STATUS_CREATED, order.GetStatus())
		assert.EqualValues(t, 2*stackBreadCost+stackMilkCost, order.GetTotalAmount())
		require.Len(t, order.GetItems(), 2)

		assert.Equal(t, stackRowCounts{orders: 1, items: 2, events: 1}, countStackRows(t, pool))

		var userID, orderStatus string
		require.NoError(t, pool.QueryRow(t.Context(),
			`SELECT user_id::text, status FROM orders WHERE id = $1`, order.GetId()).Scan(&userID, &orderStatus))
		assert.Equal(t, stackUserID, userID)
		assert.Equal(t, "created", orderStatus)

		var aggregateID, eventType string
		require.NoError(t, pool.QueryRow(t.Context(),
			`SELECT aggregate_id::text, type FROM outbox_events`).Scan(&aggregateID, &eventType))
		assert.Equal(t, order.GetId(), aggregateID)
		assert.Equal(t, domain.EventTypeOrderCreated, eventType)
	})

	t.Run("dependency errors map to gRPC codes and leave database empty", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			deps *fakeDeps
			want codes.Code
		}{
			{
				name: "user not found",
				deps: &fakeDeps{userMissing: true, catalog: defaultCatalog()},
				want: codes.NotFound,
			},
			{
				name: "address not found",
				deps: &fakeDeps{addressMissing: true, catalog: defaultCatalog()},
				want: codes.NotFound,
			},
			{
				name: "product service returns not found",
				deps: &fakeDeps{productsError: status.Error(codes.NotFound, "no products")},
				want: codes.NotFound,
			},
			{
				name: "product missing from catalog",
				deps: &fakeDeps{catalog: map[string]*productv1.Product{stackBreadID: defaultCatalog()[stackBreadID]}},
				want: codes.NotFound,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				cli, pool := newOrderStack(t, tt.deps)

				_, err := cli.CreateOrder(t.Context(), stackRequest())

				require.Error(t, err)
				assert.Equal(t, tt.want, status.Code(err))
				assert.Equal(t, stackRowCounts{}, countStackRows(t, pool))
			})
		}
	})

	t.Run("invalid request is rejected by validation interceptor", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*orderv1.CreateOrderRequest)
		}{
			{"user id is not uuid", func(r *orderv1.CreateOrderRequest) { r.UserId = "not-a-uuid" }},
			{"address id is not uuid", func(r *orderv1.CreateOrderRequest) { r.AddressId = "" }},
			{"no items", func(r *orderv1.CreateOrderRequest) { r.Items = nil }},
			{"zero quantity", func(r *orderv1.CreateOrderRequest) { r.Items[0].Quantity = 0 }},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				deps := &fakeDeps{catalog: defaultCatalog()}
				cli, pool := newOrderStack(t, deps)
				req := stackRequest()
				tt.mutate(req)

				_, err := cli.CreateOrder(t.Context(), req)

				require.Error(t, err)
				assert.Equal(t, codes.InvalidArgument, status.Code(err))
				assert.Zero(t, deps.calls.Load(), "validation must reject before any dependency call")
				assert.Equal(t, stackRowCounts{}, countStackRows(t, pool))
			})
		}
	})
}
