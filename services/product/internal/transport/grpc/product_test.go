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
	productv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/product/v1"

	"github.com/maksimegorovdev/delivery-backend/services/product/internal/domain"
	grpctransport "github.com/maksimegorovdev/delivery-backend/services/product/internal/transport/grpc"
	"github.com/maksimegorovdev/delivery-backend/services/product/internal/transport/grpc/mocks"
)

const (
	productID1 = "0197a1b2-0000-7000-8000-000000000001"
	productID2 = "0197a1b2-0000-7000-8000-000000000002"
)

// newClient поднимает роутер с теми же валидацией и обработкой ошибок, в том же порядке, что и app сервиса.
func newClient(t *testing.T, svc grpctransport.ProductService) productv1.ProductServiceClient {
	t.Helper()

	validator, err := protovalidate.New()
	require.NoError(t, err)

	conn := grpctest.NewConn(t,
		func(s *grpc.Server) {
			grpctransport.NewRouter(grpctransport.RouterDeps{Server: s, ProductService: svc})
		},
		grpc.ChainUnaryInterceptor(
			interceptors.Error(),
			interceptors.Validation(validator),
		),
	)

	return productv1.NewProductServiceClient(conn)
}

func TestProductRouter_GetProducts_Success(t *testing.T) {
	t.Parallel()

	ids := []string{productID1, productID2}
	// Времена различаются, чтобы перепутанные CreatedAt/UpdatedAt не прошли незамеченными.
	products := []domain.Product{
		{
			ID:        productID1,
			Name:      "Pizza",
			Price:     500,
			CreatedAt: time.Date(2025, time.March, 1, 10, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2025, time.March, 2, 11, 30, 0, 0, time.UTC),
		},
		{
			ID:        productID2,
			Name:      "Soup",
			Price:     300,
			CreatedAt: time.Date(2025, time.April, 1, 10, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2025, time.April, 2, 11, 30, 0, 0, time.UTC),
		},
	}
	svc := mocks.NewMockProductService(t)
	svc.EXPECT().GetProducts(mock.Anything, ids).Return(products, nil)
	client := newClient(t, svc)

	resp, err := client.GetProducts(t.Context(), &productv1.GetProductsRequest{Ids: ids})

	require.NoError(t, err)
	got := resp.GetProducts()
	require.Len(t, got, len(products))
	for i, want := range products {
		assert.Equal(t, want.ID, got[i].GetId())
		assert.Equal(t, want.Name, got[i].GetName())
		assert.Equal(t, want.Price, got[i].GetPrice())
		assert.True(t, want.CreatedAt.Equal(got[i].GetCreatedAt().AsTime()))
		assert.True(t, want.UpdatedAt.Equal(got[i].GetUpdatedAt().AsTime()))
	}
}

func TestProductRouter_GetProducts_EmptyResult(t *testing.T) {
	t.Parallel()

	ids := []string{productID1}
	svc := mocks.NewMockProductService(t)
	svc.EXPECT().GetProducts(mock.Anything, ids).Return(nil, nil)
	client := newClient(t, svc)

	resp, err := client.GetProducts(t.Context(), &productv1.GetProductsRequest{Ids: ids})

	require.NoError(t, err)
	assert.Empty(t, resp.GetProducts())
}

func TestProductRouter_GetProducts_ServiceError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		wantCode codes.Code
	}{
		{name: "not found", err: apperr.NotFound(), wantCode: codes.NotFound},
		{name: "invalid argument", err: apperr.InvalidArgument(), wantCode: codes.InvalidArgument},
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

			ids := []string{productID1}
			svc := mocks.NewMockProductService(t)
			svc.EXPECT().GetProducts(mock.Anything, ids).Return(nil, tt.err)
			client := newClient(t, svc)

			_, err := client.GetProducts(t.Context(), &productv1.GetProductsRequest{Ids: ids})

			require.Error(t, err)
			assert.Equal(t, tt.wantCode, status.Code(err))
		})
	}
}

func TestProductRouter_GetProducts_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ids  []string
	}{
		{name: "ids is empty", ids: nil},
		{name: "id is not a uuid", ids: []string{"not-a-uuid"}},
		{name: "ids contain duplicates", ids: []string{productID1, productID1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Мок без ожиданий: любой вызов сервиса провалит тест.
			svc := mocks.NewMockProductService(t)
			client := newClient(t, svc)

			_, err := client.GetProducts(t.Context(), &productv1.GetProductsRequest{Ids: tt.ids})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}
