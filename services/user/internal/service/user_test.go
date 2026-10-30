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

func TestUserService_GetUser(t *testing.T) {
	t.Parallel()

	errDB := errors.New("db is down")
	user := domain.User{
		ID:        "user-1",
		Email:     "user@example.com",
		FirstName: "Ivan",
		LastName:  "Petrov",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}

	tests := []struct {
		name    string
		setup   func(repo *mocks.MockUserRepo)
		want    domain.User
		wantErr error
	}{
		{
			name: "returns user from repository",
			setup: func(repo *mocks.MockUserRepo) {
				repo.EXPECT().GetByID(mock.Anything, "user-1").Return(user, nil)
			},
			want: user,
		},
		{
			name: "propagates repository error",
			setup: func(repo *mocks.MockUserRepo) {
				repo.EXPECT().GetByID(mock.Anything, "user-1").Return(domain.User{}, errDB)
			},
			wantErr: errDB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			repo := mocks.NewMockUserRepo(t)
			tt.setup(repo)

			got, err := service.NewUserService(repo).GetUser(context.Background(), "user-1")

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
