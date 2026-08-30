package grpcserver

import (
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
	notify          chan error
}

func New(opts ...Option) *Server {
	srv := &Server{
		server:          grpc.NewServer(),
		host:            defaultHost,
		port:            defaultPort,
		shutdownTimeout: defaultShutdownTimeout,
		notify:          make(chan error, 1),
	}

	for _, opt := range opts {
		opt(srv)
	}

	return srv
}

func (s *Server) Server() *grpc.Server {
	return s.server
}

func (s *Server) Notify() <-chan error {
	return s.notify
}

func (s *Server) Start() error {
	lis, err := net.Listen("tcp", net.JoinHostPort(s.host, strconv.Itoa(s.port)))
	if err != nil {
		return fmt.Errorf("grpcserver: listen: %w", err)
	}

	go func() {
		if err := s.server.Serve(lis); err != nil {
			s.notify <- fmt.Errorf("grpcserver: serve: %w", err)
		}
		close(s.notify)
	}()

	return nil
}

func (s *Server) Shutdown() error {
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
		return fmt.Errorf("grpcserver: graceful stop timed out after %s: forced stop", s.shutdownTimeout)
	}
}
