package grpc

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	addressv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/address/v1"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type AddressUsecase interface {
	GetAddress(ctx context.Context, id string) (domain.Address, error)
}

func addressToProto(a domain.Address) *addressv1.Address {
	return &addressv1.Address{
		Id:        a.ID,
		UserId:    a.UserID,
		Address:   a.Address,
		CreatedAt: timestamppb.New(a.CreatedAt),
		UpdatedAt: timestamppb.New(a.UpdatedAt),
	}
}

type AddressRouter struct {
	addressv1.UnimplementedAddressServiceServer
	uc AddressUsecase
}

func NewAddressRoutes(server *grpc.Server, uc AddressUsecase) {
	addressv1.RegisterAddressServiceServer(server, &AddressRouter{uc: uc})
}

func (r *AddressRouter) GetAddress(ctx context.Context, req *addressv1.GetAddressRequest) (*addressv1.GetAddressResponse, error) {
	address, err := r.uc.GetAddress(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &addressv1.GetAddressResponse{Address: addressToProto(address)}, nil
}
