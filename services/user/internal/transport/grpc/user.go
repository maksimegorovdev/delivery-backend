package grpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	userv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/user/v1"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/domain"
)

type UserUsecase interface {
	GetUser(ctx context.Context, id string) (domain.User, error)
}

func userToProto(u domain.User) *userv1.User {
	return &userv1.User{
		Id:        u.ID,
		Email:     u.Email,
		FirstName: u.FirstName,
		LastName:  u.LastName,
		CreatedAt: timestamppb.New(u.CreatedAt),
		UpdatedAt: timestamppb.New(u.UpdatedAt),
	}
}

type UserHandler struct {
	userv1.UnimplementedUserServiceServer
	uc UserUsecase
}

func NewUserHandler(uc UserUsecase) *UserHandler {
	return &UserHandler{uc: uc}
}

func (h *UserHandler) GetUser(ctx context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	u, err := h.uc.GetUser(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return &userv1.GetUserResponse{User: userToProto(u)}, nil
}
