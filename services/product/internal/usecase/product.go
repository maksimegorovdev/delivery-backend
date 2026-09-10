package usecase

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/product/internal/domain"
)

type ProductRepo interface {
	GetByIDs(ctx context.Context, ids []string) ([]domain.Product, error)
}

type ProductUsecase struct {
	repo ProductRepo
}

func NewProductUsecase(productRepo ProductRepo) *ProductUsecase {
	return &ProductUsecase{repo: productRepo}
}

func (uc *ProductUsecase) GetProducts(ctx context.Context, ids []string) ([]domain.Product, error) {
	return uc.repo.GetByIDs(ctx, ids)
}
