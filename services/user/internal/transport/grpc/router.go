package grpc

import (
	"google.golang.org/grpc"
)

type RouterDeps struct {
	Server         *grpc.Server
	UserService    UserService
	AddressService AddressService
}

func NewRouter(deps RouterDeps) {
	NewUserRouter(deps.Server, deps.UserService)
	NewAddressRouter(deps.Server, deps.AddressService)
}
