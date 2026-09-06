package usecase

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type UserRepository interface {
	GetByID(ctx context.Context, id string) (domain.User, error)
}

type UserUsecase struct {
	repo UserRepository
}

func NewUserUsecase(userRepo UserRepository) *UserUsecase {
	return &UserUsecase{repo: userRepo}
}

func (uc *UserUsecase) GetUser(ctx context.Context, id string) (domain.User, error) {
	return uc.repo.GetByID(ctx, id)
}
