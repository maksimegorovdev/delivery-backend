package grpc

import (
	"google.golang.org/grpc"
)

type RouterDeps struct {
	Server         *grpc.Server
	ProductUsecase ProductUsecase
}

func NewRouter(deps RouterDeps) {
	NewProductRouter(deps.Server, deps.ProductUsecase)
}
