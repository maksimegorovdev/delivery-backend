package grpcserver

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/sync/errgroup"
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
}

func New(opts ...Option) *Server {
	srv := &Server{
		server:          grpc.NewServer(),
		host:            defaultHost,
		port:            defaultPort,
		shutdownTimeout: defaultShutdownTimeout,
	}

	for _, opt := range opts {
		opt(srv)
	}

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

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		if err := s.server.Serve(lis); err != nil {
			return fmt.Errorf("grpcserver: serve: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-ctx.Done()
		return s.shutdown()
	})

	return g.Wait()
}

func (s *Server) shutdown() error {
	stopped := make(chan struct{})
	go func() {
		s.server.GracefulStop()
		close(stopped)
	}()

	timer := time.NewTimer(s.shutdownTimeout)
	defer timer.Stop()

	select {
	case <-stopped:
		return nil
	case <-timer.C:
		s.server.Stop()
		return fmt.Errorf("grpcserver: graceful shutdown timeout after %s: forced stop", s.shutdownTimeout)
	}
}
