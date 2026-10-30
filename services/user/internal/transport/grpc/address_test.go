package grpc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	addressv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/address/v1"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/transport/grpc/mocks"
)

func TestAddressRouter_GetAddress_Success(t *testing.T) {
	t.Parallel()

	address := domain.Address{
		ID:        addressID,
		UserID:    userID,
		Address:   "Lenina 1, 10",
		CreatedAt: createdAt(),
		UpdatedAt: updatedAt(),
	}
	svc := mocks.NewMockAddressService(t)
	svc.EXPECT().GetAddress(mock.Anything, addressID).Return(address, nil)
	client := newAddressClient(t, svc)

	resp, err := client.GetAddress(t.Context(), &addressv1.GetAddressRequest{Id: addressID})

	require.NoError(t, err)
	got := resp.GetAddress()
	assert.Equal(t, address.ID, got.GetId())
	assert.Equal(t, address.UserID, got.GetUserId())
	assert.Equal(t, address.Address, got.GetAddress())
	assert.True(t, address.CreatedAt.Equal(got.GetCreatedAt().AsTime()))
	assert.True(t, address.UpdatedAt.Equal(got.GetUpdatedAt().AsTime()))
}

func TestAddressRouter_GetAddress_ServiceError(t *testing.T) {
	t.Parallel()

	for _, tt := range errorCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := mocks.NewMockAddressService(t)
			svc.EXPECT().GetAddress(mock.Anything, addressID).Return(domain.Address{}, tt.err)
			client := newAddressClient(t, svc)

			_, err := client.GetAddress(t.Context(), &addressv1.GetAddressRequest{Id: addressID})

			require.Error(t, err)
			assert.Equal(t, tt.wantCode, status.Code(err))
		})
	}
}

func TestAddressRouter_GetAddress_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
	}{
		{name: "id is empty", id: ""},
		{name: "id is not a uuid", id: "not-a-uuid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Мок без ожиданий: любой вызов сервиса провалит тест.
			svc := mocks.NewMockAddressService(t)
			client := newAddressClient(t, svc)

			_, err := client.GetAddress(t.Context(), &addressv1.GetAddressRequest{Id: tt.id})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}
