package usecase

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type UserRepo interface {
	GetByID(ctx context.Context, id string) (domain.User, error)
}

type UserUsecase struct {
	users UserRepo
}

func NewUserUsecase(users UserRepo) *UserUsecase {
	return &UserUsecase{users: users}
}

func (uc *UserUsecase) GetUser(ctx context.Context, id string) (domain.User, error) {
	return uc.users.GetByID(ctx, id)
}
