package grpc

import (
	"google.golang.org/grpc"
)

type RouterDeps struct {
	Server         *grpc.Server
	ProductUsecase ProductUsecase
}

func NewRouter(deps *RouterDeps) {
	NewProductRoutes(deps.Server, deps.ProductUsecase)
}
