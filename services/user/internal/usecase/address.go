package usecase

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type AddressRepo interface {
	GetByID(ctx context.Context, id string) (domain.Address, error)
}

type AddressUsecase struct {
	addresses AddressRepo
}

func NewAddressUsecase(addresses AddressRepo) *AddressUsecase {
	return &AddressUsecase{addresses: addresses}
}

func (uc *AddressUsecase) GetAddress(ctx context.Context, id string) (domain.Address, error) {
	return uc.addresses.GetByID(ctx, id)
}
