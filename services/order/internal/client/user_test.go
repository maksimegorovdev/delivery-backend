package client_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/grpctest"
	addressv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/address/v1"
	userv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/user/v1"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/client"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

type fakeUserServer struct {
	userv1.UnimplementedUserServiceServer

	gotID string
	user  *userv1.User
	err   error
}

func (f *fakeUserServer) GetUser(_ context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	f.gotID = req.GetId()
	if f.err != nil {
		return nil, f.err
	}

	return &userv1.GetUserResponse{User: f.user}, nil
}

type fakeAddressServer struct {
	addressv1.UnimplementedAddressServiceServer

	gotID   string
	address *addressv1.Address
	err     error
}

func (f *fakeAddressServer) GetAddress(
	_ context.Context,
	req *addressv1.GetAddressRequest,
) (*addressv1.GetAddressResponse, error) {
	f.gotID = req.GetId()
	if f.err != nil {
		return nil, f.err
	}

	return &addressv1.GetAddressResponse{Address: f.address}, nil
}

func newUserClient(t *testing.T, users *fakeUserServer, addresses *fakeAddressServer) *client.UserClient {
	t.Helper()

	conn := grpctest.NewConn(t, func(s *grpc.Server) {
		userv1.RegisterUserServiceServer(s, users)
		addressv1.RegisterAddressServiceServer(s, addresses)
	})

	return client.NewUserClient(conn)
}

func TestUserClient_GetUser(t *testing.T) {
	t.Parallel()

	t.Run("maps all proto fields to domain and sends the id", func(t *testing.T) {
		t.Parallel()

		srv := &fakeUserServer{user: &userv1.User{
			Id:        "user-1",
			Email:     "ivan@example.com",
			FirstName: "Ivan",
			LastName:  "Petrov",
			CreatedAt: ts(createdAt()),
			UpdatedAt: ts(updatedAt()),
		}}
		c := newUserClient(t, srv, &fakeAddressServer{})

		got, err := c.GetUser(t.Context(), "user-1")

		require.NoError(t, err)
		assert.Equal(t, "user-1", srv.gotID)
		assert.Equal(t, domain.User{
			ID:        "user-1",
			Email:     "ivan@example.com",
			FirstName: "Ivan",
			LastName:  "Petrov",
			CreatedAt: createdAt(),
			UpdatedAt: updatedAt(),
		}, got)
	})

	t.Run("maps grpc not found to apperr not found", func(t *testing.T) {
		t.Parallel()

		srv := &fakeUserServer{err: errNotFound()}
		c := newUserClient(t, srv, &fakeAddressServer{})

		got, err := c.GetUser(t.Context(), "user-404")

		requireNotFound(t, err)
		assert.Equal(t, "user-404", srv.gotID)
		assert.Equal(t, domain.User{}, got)
	})
}

func TestUserClient_GetAddress(t *testing.T) {
	t.Parallel()

	t.Run("maps all proto fields to domain and sends the id", func(t *testing.T) {
		t.Parallel()

		srv := &fakeAddressServer{address: &addressv1.Address{
			Id:        "addr-1",
			UserId:    "user-1",
			Address:   "Lenina st, 1",
			CreatedAt: ts(createdAt()),
			UpdatedAt: ts(updatedAt()),
		}}
		c := newUserClient(t, &fakeUserServer{}, srv)

		got, err := c.GetAddress(t.Context(), "addr-1")

		require.NoError(t, err)
		assert.Equal(t, "addr-1", srv.gotID)
		assert.Equal(t, domain.Address{
			ID:        "addr-1",
			UserID:    "user-1",
			Address:   "Lenina st, 1",
			CreatedAt: createdAt(),
			UpdatedAt: updatedAt(),
		}, got)
	})

	t.Run("maps grpc not found to apperr not found", func(t *testing.T) {
		t.Parallel()

		srv := &fakeAddressServer{err: errNotFound()}
		c := newUserClient(t, &fakeUserServer{}, srv)

		got, err := c.GetAddress(t.Context(), "addr-404")

		requireNotFound(t, err)
		assert.Equal(t, "addr-404", srv.gotID)
		assert.Equal(t, domain.Address{}, got)
	})
}
