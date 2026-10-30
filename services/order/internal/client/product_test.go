package client_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/grpctest"
	productv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/product/v1"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/client"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

type fakeProductServer struct {
	productv1.UnimplementedProductServiceServer

	gotIDs   []string
	products []*productv1.Product
	err      error
}

func (f *fakeProductServer) GetProducts(
	_ context.Context,
	req *productv1.GetProductsRequest,
) (*productv1.GetProductsResponse, error) {
	f.gotIDs = req.GetIds()
	if f.err != nil {
		return nil, f.err
	}

	return &productv1.GetProductsResponse{Products: f.products}, nil
}

func newProductClient(t *testing.T, srv *fakeProductServer) *client.ProductClient {
	t.Helper()

	conn := grpctest.NewConn(t, func(s *grpc.Server) {
		productv1.RegisterProductServiceServer(s, srv)
	})

	return client.NewProductClient(conn)
}

func TestProductClient_GetProducts(t *testing.T) {
	t.Parallel()

	t.Run("maps all proto fields to domain and sends the ids", func(t *testing.T) {
		t.Parallel()

		srv := &fakeProductServer{products: []*productv1.Product{
			{Id: "p-1", Name: "Pizza", Price: 1200, CreatedAt: ts(createdAt()), UpdatedAt: ts(updatedAt())},
			{Id: "p-2", Name: "Cola", Price: 150, CreatedAt: ts(createdAt()), UpdatedAt: ts(updatedAt())},
		}}
		c := newProductClient(t, srv)

		got, err := c.GetProducts(t.Context(), []string{"p-1", "p-2"})

		require.NoError(t, err)
		assert.Equal(t, []string{"p-1", "p-2"}, srv.gotIDs)
		assert.Equal(t, []domain.Product{
			{ID: "p-1", Name: "Pizza", Price: 1200, CreatedAt: createdAt(), UpdatedAt: updatedAt()},
			{ID: "p-2", Name: "Cola", Price: 150, CreatedAt: createdAt(), UpdatedAt: updatedAt()},
		}, got)
	})

	t.Run("returns empty non-nil slice when server returns no products", func(t *testing.T) {
		t.Parallel()

		c := newProductClient(t, &fakeProductServer{})

		got, err := c.GetProducts(t.Context(), []string{"p-1"})

		require.NoError(t, err)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})

	t.Run("maps grpc not found to apperr not found", func(t *testing.T) {
		t.Parallel()

		srv := &fakeProductServer{err: errNotFound()}
		c := newProductClient(t, srv)

		got, err := c.GetProducts(t.Context(), []string{"p-404"})

		requireNotFound(t, err)
		assert.Equal(t, []string{"p-404"}, srv.gotIDs)
		assert.Nil(t, got)
	})
}
