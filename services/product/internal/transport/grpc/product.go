package grpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	productv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/product/v1"
	"github.com/maksimegorovdev/delivery-backend/services/product/internal/domain"
)

type ProductUsecase interface {
	GetProducts(ctx context.Context, ids []string) ([]domain.Product, error)
}

func productToProto(p domain.Product) *productv1.Product {
	return &productv1.Product{
		Id:        p.ID,
		Name:      p.Name,
		Price:     p.Price,
		CreatedAt: timestamppb.New(p.CreatedAt),
		UpdatedAt: timestamppb.New(p.UpdatedAt),
	}
}

type ProductRouter struct {
	productv1.UnimplementedProductServiceServer
	uc ProductUsecase
}

func NewProductRoutes(server *grpc.Server, uc ProductUsecase) {
	productv1.RegisterProductServiceServer(server, &ProductRouter{uc: uc})
}

func (r *ProductRouter) GetProducts(ctx context.Context, req *productv1.GetProductsRequest) (*productv1.GetProductsResponse, error) {
	list, err := r.uc.GetProducts(ctx, req.GetIds())
	if err != nil {
		return nil, err
	}

	resp := &productv1.GetProductsResponse{
		Products: make([]*productv1.Product, 0, len(list)),
	}
	for _, product := range list {
		resp.Products = append(resp.Products, productToProto(product))
	}
	return resp, nil
}
