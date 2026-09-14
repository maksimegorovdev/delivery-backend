package client

import (
	"context"

	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr/grpcerr"
	addressv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/address/v1"
	userv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/user/v1"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

func protoToUser(p *userv1.User) domain.User {
	return domain.User{
		ID:        p.GetId(),
		Email:     p.GetEmail(),
		FirstName: p.GetFirstName(),
		LastName:  p.GetLastName(),
		CreatedAt: p.GetCreatedAt().AsTime(),
		UpdatedAt: p.GetUpdatedAt().AsTime(),
	}
}

func protoToAddress(p *addressv1.Address) domain.Address {
	return domain.Address{
		ID:        p.GetId(),
		UserID:    p.GetUserId(),
		Address:   p.GetAddress(),
		CreatedAt: p.GetCreatedAt().AsTime(),
		UpdatedAt: p.GetUpdatedAt().AsTime(),
	}
}

type UserClient struct {
	users     userv1.UserServiceClient
	addresses addressv1.AddressServiceClient
}

func NewUserClient(conn *grpc.ClientConn) *UserClient {
	return &UserClient{
		users:     userv1.NewUserServiceClient(conn),
		addresses: addressv1.NewAddressServiceClient(conn),
	}
}

func (c *UserClient) GetUser(ctx context.Context, id string) (domain.User, error) {
	resp, err := c.users.GetUser(ctx, &userv1.GetUserRequest{
		Id: id,
	})
	if err != nil {
		return domain.User{}, grpcerr.Map(err)
	}
	return protoToUser(resp.GetUser()), nil
}

func (c *UserClient) GetAddress(ctx context.Context, id string) (domain.Address, error) {
	resp, err := c.addresses.GetAddress(ctx, &addressv1.GetAddressRequest{
		Id: id,
	})
	if err != nil {
		return domain.Address{}, grpcerr.Map(err)
	}
	return protoToAddress(resp.GetAddress()), nil
}
