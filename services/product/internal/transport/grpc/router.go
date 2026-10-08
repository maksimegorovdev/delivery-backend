package grpc

import (
	"google.golang.org/grpc"
)

type RouterDeps struct {
	Server         *grpc.Server
	ProductService ProductService
}

func NewRouter(deps RouterDeps) {
	NewProductRouter(deps.Server, deps.ProductService)
}
