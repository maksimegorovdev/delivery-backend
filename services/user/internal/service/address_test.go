package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/service/mocks"
)

func TestAddressService_GetAddress(t *testing.T) {
	t.Parallel()

	errDB := errors.New("db is down")
	address := domain.Address{
		ID:        "address-1",
		UserID:    "user-1",
		Address:   "Main st, 1",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}

	tests := []struct {
		name    string
		setup   func(repo *mocks.MockAddressRepo)
		want    domain.Address
		wantErr error
	}{
		{
			name: "returns address from repository",
			setup: func(repo *mocks.MockAddressRepo) {
				repo.EXPECT().GetByID(mock.Anything, "address-1").Return(address, nil)
			},
			want: address,
		},
		{
			name: "propagates repository error",
			setup: func(repo *mocks.MockAddressRepo) {
				repo.EXPECT().GetByID(mock.Anything, "address-1").Return(domain.Address{}, errDB)
			},
			wantErr: errDB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := mocks.NewMockAddressRepo(t)
			tt.setup(repo)

			got, err := service.NewAddressService(repo).GetAddress(context.Background(), "address-1")

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
