package service

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/services/product/internal/domain"
)

type ProductRepo interface {
	GetByIDs(ctx context.Context, ids []string) ([]domain.Product, error)
}

type ProductService struct {
	products ProductRepo
}

func NewProductService(products ProductRepo) *ProductService {
	return &ProductService{products: products}
}

func (uc *ProductService) GetProducts(ctx context.Context, ids []string) ([]domain.Product, error) {
	return uc.products.GetByIDs(ctx, ids)
}
