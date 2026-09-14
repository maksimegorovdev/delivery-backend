package grpcserver

import (
	"time"

	"google.golang.org/grpc"
)

type Option func(*Server)

func WithServerOptions(opts ...grpc.ServerOption) Option {
	return func(s *Server) {
		s.serverOpts = append(s.serverOpts, opts...)
	}
}

func WithShutdownTimeout(timeout time.Duration) Option {
	return func(s *Server) {
		s.shutdownTimeout = timeout
	}
}

func WithReflection(enabled bool) Option {
	return func(s *Server) {
		s.reflection = enabled
	}
}
