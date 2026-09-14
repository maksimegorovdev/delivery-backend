package client

import (
	"context"

	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/grpcerr"
	productv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/product/v1"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

func protoToProduct(p *productv1.Product) domain.Product {
	return domain.Product{
		ID:        p.GetId(),
		Name:      p.GetName(),
		Price:     p.GetPrice(),
		CreatedAt: p.GetCreatedAt().AsTime(),
		UpdatedAt: p.GetUpdatedAt().AsTime(),
	}
}

type ProductClient struct {
	products productv1.ProductServiceClient
}

func NewProductClient(conn *grpc.ClientConn) *ProductClient {
	return &ProductClient{
		products: productv1.NewProductServiceClient(conn),
	}
}

func (c *ProductClient) GetProducts(ctx context.Context, ids []string) ([]domain.Product, error) {
	resp, err := c.products.GetProducts(ctx, &productv1.GetProductsRequest{
		Ids: ids,
	})
	if err != nil {
		return nil, grpcerr.Map(err)
	}
	products := make([]domain.Product, 0, len(resp.GetProducts()))
	for _, product := range resp.GetProducts() {
		products = append(products, protoToProduct(product))
	}
	return products, nil
}
