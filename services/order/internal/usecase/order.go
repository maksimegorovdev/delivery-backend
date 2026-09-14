package usecase

import (
	"context"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

type OrderRepo interface {
	Create(ctx context.Context, order domain.Order) error
}

type UserProvider interface {
	GetUser(ctx context.Context, id string) (domain.User, error)
	GetAddress(ctx context.Context, id string) (domain.Address, error)
}

type ProductProvider interface {
	GetProducts(ctx context.Context, ids []string) ([]domain.Product, error)
}

type CreateOrderInput struct {
	UserID    string
	AddressID string
	Items     []CreateOrderItemInput
}

type CreateOrderItemInput struct {
	ProductID string
	Quantity  int32
}

type OrderUsecaseDeps struct {
	Orders   OrderRepo
	Users    UserProvider
	Products ProductProvider
}

type OrderUsecase struct {
	orders   OrderRepo
	users    UserProvider
	products ProductProvider
}

func NewOrderUsecase(deps OrderUsecaseDeps) *OrderUsecase {
	return &OrderUsecase{
		orders:   deps.Orders,
		users:    deps.Users,
		products: deps.Products,
	}
}

func (uc *OrderUsecase) CreateOrder(ctx context.Context, input CreateOrderInput) (domain.Order, error) {
	if _, err := uc.users.GetUser(ctx, input.UserID); err != nil {
		return domain.Order{}, err
	}

	addr, err := uc.users.GetAddress(ctx, input.AddressID)
	if err != nil {
		return domain.Order{}, err
	}

	ids := make([]string, len(input.Items))
	for i, item := range input.Items {
		ids[i] = item.ProductID
	}
	catalog, err := uc.products.GetProducts(ctx, ids)
	if err != nil {
		return domain.Order{}, err
	}

	byID := make(map[string]domain.Product, len(catalog))
	for _, product := range catalog {
		byID[product.ID] = product
	}

	items := make([]domain.OrderItem, 0, len(input.Items))
	for _, item := range input.Items {
		product, ok := byID[item.ProductID]
		if !ok {
			return domain.Order{}, apperr.NotFound().Wrap(domain.ErrProductNotFound)
		}
		items = append(items, domain.OrderItem{
			ProductID:   product.ID,
			ProductName: product.Name,
			Quantity:    item.Quantity,
			UnitPrice:   product.Price,
		})
	}

	order, err := domain.NewOrder(input.UserID, addr.Address, items)
	if err != nil {
		return domain.Order{}, apperr.InvalidArgument().Wrap(err)
	}

	if err := uc.orders.Create(ctx, order); err != nil {
		return domain.Order{}, err
	}
	return order, nil
}
