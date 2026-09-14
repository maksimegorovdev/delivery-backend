package grpc

import (
	"google.golang.org/grpc"
)

type RouterDeps struct {
	Server         *grpc.Server
	UserUsecase    UserUsecase
	AddressUsecase AddressUsecase
}

func NewRouter(deps RouterDeps) {
	NewUserRouter(deps.Server, deps.UserUsecase)
	NewAddressRouter(deps.Server, deps.AddressUsecase)
}
