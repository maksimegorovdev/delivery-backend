package grpc

import "google.golang.org/grpc"

type RouterDeps struct {
	Server       *grpc.Server
	OrderService OrderService
}

func NewRouter(deps RouterDeps) {
	NewOrderRouter(deps.Server, deps.OrderService)
}
