package grpcserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc/filters"
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

	srv.server = grpc.NewServer(append([]grpc.ServerOption{otelStatsHandler()}, srv.serverOpts...)...)

	if srv.reflection {
		reflection.Register(srv.server)
	}

	return srv
}

// otelStatsHandler трассирует RPC и пишет их метрики через глобальные провайдеры OTel;
// служебные reflection и health в телеметрию не попадают.
func otelStatsHandler() grpc.ServerOption {
	return grpc.StatsHandler(otelgrpc.NewServerHandler(
		otelgrpc.WithFilter(filters.None(
			filters.ServicePrefix("grpc.reflection."),
			filters.HealthCheck(),
		)),
	))
}

func (s *Server) Server() *grpc.Server {
	return s.server
}

func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("grpcserver: listen: %w", err)
	}

	return s.Serve(ctx, lis)
}

// Serve обслуживает соединения lis до отмены ctx, затем выполняет graceful stop.
// Serve закрывает lis. Run — это Serve на листенере по адресу из New; Serve нужен,
// когда листенер создаёт вызывающий (например, тест на порту, выданном ОС).
func (s *Server) Serve(ctx context.Context, lis net.Listener) error {
	serveErr := make(chan error, 1)
	go func() {
		if serr := s.server.Serve(lis); serr != nil && !errors.Is(serr, grpc.ErrServerStopped) {
			serveErr <- serr
		}
	}()

	select {
	case serr := <-serveErr:
		return fmt.Errorf("grpcserver: serve: %w", serr)
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

	select {
	case <-done:
		return nil
	case <-time.After(s.shutdownTimeout):
		s.server.Stop()
		<-done
		return fmt.Errorf("grpcserver: graceful stop timed out after %s", s.shutdownTimeout)
	}
}
