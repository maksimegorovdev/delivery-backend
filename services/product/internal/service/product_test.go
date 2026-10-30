package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/services/product/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/product/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/product/internal/service/mocks"
)

func TestProductService_GetProducts(t *testing.T) {
	t.Parallel()

	errDB := errors.New("db is down")
	ids := []string{"product-1", "product-2"}
	products := []domain.Product{
		{
			ID:        "product-1",
			Name:      "Pizza",
			Price:     500,
			CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			UpdatedAt: time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC),
		},
		{ID: "product-2", Name: "Soup", Price: 300},
	}

	tests := []struct {
		name    string
		setup   func(repo *mocks.MockProductRepo)
		want    []domain.Product
		wantErr error
	}{
		{
			name: "returns products from repository",
			setup: func(repo *mocks.MockProductRepo) {
				repo.EXPECT().GetByIDs(mock.Anything, ids).Return(products, nil)
			},
			want: products,
		},
		{
			name: "propagates repository error",
			setup: func(repo *mocks.MockProductRepo) {
				repo.EXPECT().GetByIDs(mock.Anything, ids).Return(nil, errDB)
			},
			wantErr: errDB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := mocks.NewMockProductRepo(t)
			tt.setup(repo)

			got, err := service.NewProductService(repo).GetProducts(context.Background(), ids)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
