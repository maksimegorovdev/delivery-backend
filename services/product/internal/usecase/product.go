package usecase

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/product/internal/domain"
)

type ProductRepo interface {
	GetByIDs(ctx context.Context, ids []string) ([]domain.Product, error)
}

type ProductUsecase struct {
	products ProductRepo
}

func NewProductUsecase(products ProductRepo) *ProductUsecase {
	return &ProductUsecase{products: products}
}

func (uc *ProductUsecase) GetProducts(ctx context.Context, ids []string) ([]domain.Product, error) {
	return uc.products.GetByIDs(ctx, ids)
}
