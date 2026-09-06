package grpcserver

import (
	"time"

	"google.golang.org/grpc"
)

type Option func(*Server)

func WithHost(host string) Option {
	return func(s *Server) {
		s.host = host
	}
}

func WithPort(port int) Option {
	return func(s *Server) {
		s.port = port
	}
}

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
