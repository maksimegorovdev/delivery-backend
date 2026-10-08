package service

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type UserRepo interface {
	GetByID(ctx context.Context, id string) (domain.User, error)
}

type UserService struct {
	users UserRepo
}

func NewUserService(users UserRepo) *UserService {
	return &UserService{users: users}
}

func (uc *UserService) GetUser(ctx context.Context, id string) (domain.User, error) {
	return uc.users.GetByID(ctx, id)
}
