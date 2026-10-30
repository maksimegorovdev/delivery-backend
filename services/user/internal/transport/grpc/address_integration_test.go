//go:build integration

package grpc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	addressv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/address/v1"
)

func TestUserStack_GetAddress(t *testing.T) {
	t.Parallel()

	t.Run("returns stored address", func(t *testing.T) {
		t.Parallel()

		stack := newUserStack(t)

		resp, err := stack.addresses.GetAddress(t.Context(), &addressv1.GetAddressRequest{Id: stackAddressID})

		require.NoError(t, err)
		address := resp.GetAddress()
		assert.Equal(t, stackAddressID, address.GetId())
		assert.Equal(t, stackUserID, address.GetUserId())
		assert.Equal(t, stackAddress, address.GetAddress())
		assert.Equal(t, stackCreatedAt(), address.GetCreatedAt().AsTime())
		assert.Equal(t, stackUpdatedAt(), address.GetUpdatedAt().AsTime())
	})

	t.Run("unknown id maps to not found", func(t *testing.T) {
		t.Parallel()

		stack := newUserStack(t)

		resp, err := stack.addresses.GetAddress(t.Context(), &addressv1.GetAddressRequest{Id: stackMissingID})

		require.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
		assert.Equal(t, apperr.MessageNotFound, status.Convert(err).Message())
		assert.Nil(t, resp)
	})

	t.Run("invalid id is rejected by validation interceptor", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name string
			id   string
		}{
			{name: "not a uuid", id: "not-a-uuid"},
			{name: "empty id", id: ""},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				stack := newUserStack(t)

				resp, err := stack.addresses.GetAddress(t.Context(), &addressv1.GetAddressRequest{Id: tt.id})

				require.Error(t, err)
				assert.Equal(t, codes.InvalidArgument, status.Code(err))
				// Сообщение валидатора, а не маппинга ошибки БД: до репозитория запрос не дошёл.
				assert.Equal(t, apperr.ReasonMessageValidationFailed, status.Convert(err).Message())
				assert.Nil(t, resp)
			})
		}
	})
}
