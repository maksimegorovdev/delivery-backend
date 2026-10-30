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
	addressv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/address/v1"
	userv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/user/v1"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
	grpctransport "github.com/maksimegorovdev/delivery-backend/services/user/internal/transport/grpc"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/transport/grpc/mocks"
)

const (
	userID    = "0197a1b2-0000-7000-8000-000000000001"
	addressID = "0197a1b2-0000-7000-8000-000000000002"
)

// Времена различаются, чтобы перепутанные CreatedAt/UpdatedAt не прошли незамеченными.
func createdAt() time.Time { return time.Date(2025, time.March, 1, 10, 0, 0, 0, time.UTC) }

func updatedAt() time.Time { return time.Date(2025, time.March, 2, 11, 30, 0, 0, time.UTC) }

type errorCase struct {
	name     string
	err      error
	wantCode codes.Code
}

// errorCases — общая таблица соответствия ошибок сервиса кодам gRPC для всех RPC.
func errorCases() []errorCase {
	return []errorCase{
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
}

// newConn поднимает роутер с теми же валидацией и обработкой ошибок, в том же порядке, что и app сервиса.
func newConn(t *testing.T, deps grpctransport.RouterDeps) *grpc.ClientConn {
	t.Helper()

	validator, err := protovalidate.New()
	require.NoError(t, err)

	return grpctest.NewConn(t,
		func(s *grpc.Server) {
			deps.Server = s
			grpctransport.NewRouter(deps)
		},
		grpc.ChainUnaryInterceptor(
			interceptors.Error(),
			interceptors.Validation(validator),
		),
	)
}

func newUserClient(t *testing.T, svc grpctransport.UserService) userv1.UserServiceClient {
	t.Helper()

	return userv1.NewUserServiceClient(newConn(t, grpctransport.RouterDeps{
		UserService:    svc,
		AddressService: mocks.NewMockAddressService(t),
	}))
}

func newAddressClient(t *testing.T, svc grpctransport.AddressService) addressv1.AddressServiceClient {
	t.Helper()

	return addressv1.NewAddressServiceClient(newConn(t, grpctransport.RouterDeps{
		UserService:    mocks.NewMockUserService(t),
		AddressService: svc,
	}))
}

func TestUserRouter_GetUser_Success(t *testing.T) {
	t.Parallel()

	user := domain.User{
		ID:        userID,
		Email:     "ivan@example.com",
		FirstName: "Ivan",
		LastName:  "Petrov",
		CreatedAt: createdAt(),
		UpdatedAt: updatedAt(),
	}
	svc := mocks.NewMockUserService(t)
	svc.EXPECT().GetUser(mock.Anything, userID).Return(user, nil)
	client := newUserClient(t, svc)

	resp, err := client.GetUser(t.Context(), &userv1.GetUserRequest{Id: userID})

	require.NoError(t, err)
	got := resp.GetUser()
	assert.Equal(t, user.ID, got.GetId())
	assert.Equal(t, user.Email, got.GetEmail())
	assert.Equal(t, user.FirstName, got.GetFirstName())
	assert.Equal(t, user.LastName, got.GetLastName())
	assert.True(t, user.CreatedAt.Equal(got.GetCreatedAt().AsTime()))
	assert.True(t, user.UpdatedAt.Equal(got.GetUpdatedAt().AsTime()))
}

func TestUserRouter_GetUser_ServiceError(t *testing.T) {
	t.Parallel()

	for _, tt := range errorCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := mocks.NewMockUserService(t)
			svc.EXPECT().GetUser(mock.Anything, userID).Return(domain.User{}, tt.err)
			client := newUserClient(t, svc)

			_, err := client.GetUser(t.Context(), &userv1.GetUserRequest{Id: userID})

			require.Error(t, err)
			assert.Equal(t, tt.wantCode, status.Code(err))
		})
	}
}

func TestUserRouter_GetUser_Validation(t *testing.T) {
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
			svc := mocks.NewMockUserService(t)
			client := newUserClient(t, svc)

			_, err := client.GetUser(t.Context(), &userv1.GetUserRequest{Id: tt.id})

			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}
