package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

const (
	defaultShutdownTimeout = 10 * time.Second
)

type Server struct {
	server          *grpc.Server
	addr            string
	serverOpts      []grpc.ServerOption
	shutdownTimeout time.Duration
	reflection      bool
}

func New(addr string, opts ...Option) *Server {
	srv := &Server{
		addr:            addr,
		shutdownTimeout: defaultShutdownTimeout,
	}

	for _, opt := range opts {
		opt(srv)
	}

	srv.server = grpc.NewServer(srv.serverOpts...)

	if srv.reflection {
		reflection.Register(srv.server)
	}

	return srv
}

func (s *Server) Server() *grpc.Server {
	return s.server
}

func (s *Server) Run(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("grpcserver: listen: %w", err)
	}

	serveErr := make(chan error, 1)
	go func() {
		if err := s.server.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("grpcserver: serve: %w", err)
	case <-ctx.Done():
		return s.graceful()
	}
}

func (s *Server) graceful() error {
	done := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(done)
	}()

	timer := time.NewTimer(s.shutdownTimeout)
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-timer.C:
		s.server.Stop()
		<-done
		return fmt.Errorf("grpcserver: graceful stop timed out after %s", s.shutdownTimeout)
	}
}
