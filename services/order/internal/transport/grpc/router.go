package grpc

import "google.golang.org/grpc"

type RouterDeps struct {
	Server *grpc.Server
}

func NewRouter(deps *RouterDeps) {
}
