package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"google.golang.org/grpc"
)

const (
	defaultHost            = ""
	defaultPort            = 8090
	defaultShutdownTimeout = 10 * time.Second
)

type Server struct {
	server          *grpc.Server
	host            string
	port            int
	shutdownTimeout time.Duration
	serverOpts      []grpc.ServerOption
}

func New(opts ...Option) *Server {
	srv := &Server{
		host:            defaultHost,
		port:            defaultPort,
		shutdownTimeout: defaultShutdownTimeout,
	}

	for _, opt := range opts {
		opt(srv)
	}

	srv.server = grpc.NewServer(srv.serverOpts...)

	return srv
}

func (s *Server) Server() *grpc.Server {
	return s.server
}

func (s *Server) Run(ctx context.Context) error {
	lis, err := net.Listen("tcp", net.JoinHostPort(s.host, strconv.Itoa(s.port)))
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
