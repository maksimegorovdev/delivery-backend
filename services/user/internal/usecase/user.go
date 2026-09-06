package usecase

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type UserRepo interface {
	GetByID(ctx context.Context, id string) (domain.User, error)
}

type UserUsecase struct {
	repo UserRepo
}

func NewUserUsecase(userRepo UserRepo) *UserUsecase {
	return &UserUsecase{repo: userRepo}
}

func (uc *UserUsecase) GetUser(ctx context.Context, id string) (domain.User, error) {
	return uc.repo.GetByID(ctx, id)
}
