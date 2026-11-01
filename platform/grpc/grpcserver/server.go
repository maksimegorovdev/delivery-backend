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
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

const (
	defaultShutdownTimeout = 10 * time.Second
)

type Server struct {
	server          *grpc.Server
	serverOpts      []grpc.ServerOption
	health          *healthServer
	shutdownTimeout time.Duration
	reflection      bool
}

func New(opts ...Option) *Server {
	srv := &Server{
		shutdownTimeout: defaultShutdownTimeout,
	}

	for _, opt := range opts {
		opt(srv)
	}

	srv.server = grpc.NewServer(append([]grpc.ServerOption{otelStatsHandler()}, srv.serverOpts...)...)

	srv.health = newHealthServer()
	grpc_health_v1.RegisterHealthServer(srv.server, srv.health)

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

// Serve обслуживает соединения lis до отмены ctx, затем выполняет graceful stop.
// Сервер сам регистрирует gRPC health (общий статус SERVING). При остановке health переходит в
// NOT_SERVING, а открытые Watch-стримы получают этот статус и закрываются, не задерживая graceful stop.
// Листенер создаёт вызывающий (обычно через listener.Listen); Serve закрывает lis.
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
	// Health переводится в NOT_SERVING до остановки, чтобы клиенты и проверки узнали о выключении;
	// Watch-стримы при этом закрываются, иначе GracefulStop ждал бы их до таймаута.
	s.health.shutdown()

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
