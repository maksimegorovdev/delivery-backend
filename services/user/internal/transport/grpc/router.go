package grpc

import (
	"google.golang.org/grpc"
)

type RouterDeps struct {
	Server         *grpc.Server
	UserUsecase    UserUsecase
	AddressUsecase AddressUsecase
}

func NewRouter(deps *RouterDeps) {
	NewUserRoutes(deps.Server, deps.UserUsecase)
	NewAddressRoutes(deps.Server, deps.AddressUsecase)
}
