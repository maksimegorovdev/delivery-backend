package service

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type AddressRepo interface {
	GetByID(ctx context.Context, id string) (domain.Address, error)
}

type AddressService struct {
	addresses AddressRepo
}

func NewAddressService(addresses AddressRepo) *AddressService {
	return &AddressService{addresses: addresses}
}

func (uc *AddressService) GetAddress(ctx context.Context, id string) (domain.Address, error) {
	return uc.addresses.GetByID(ctx, id)
}
