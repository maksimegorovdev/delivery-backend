package grpc

import "google.golang.org/grpc"

type RouterDeps struct {
	Server       *grpc.Server
	OrderUsecase OrderUsecase
}

func NewRouter(deps RouterDeps) {
	NewOrderRouter(deps.Server, deps.OrderUsecase)
}
