package usecase

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type AddressRepo interface {
	GetByID(ctx context.Context, id string) (domain.Address, error)
}

type AddressUsecase struct {
	repo AddressRepo
}

func NewAddressUsecase(addressRepo AddressRepo) *AddressUsecase {
	return &AddressUsecase{repo: addressRepo}
}

func (uc *AddressUsecase) GetAddress(ctx context.Context, id string) (domain.Address, error) {
	return uc.repo.GetByID(ctx, id)
}
