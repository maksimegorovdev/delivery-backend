package interceptors_test

import (
	"context"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/grpctest"
)

// checkFunc — поведение Check тестового сервиса.
type checkFunc func(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error)

// healthService — тестовый gRPC-сервис: поведение Check задаётся тестом, вызовы считаются.
type healthService struct {
	grpc_health_v1.UnimplementedHealthServer

	check checkFunc

	mu    sync.Mutex
	calls int
}

func (s *healthService) Check(
	ctx context.Context,
	req *grpc_health_v1.HealthCheckRequest,
) (*grpc_health_v1.HealthCheckResponse, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()

	return s.check(ctx, req)
}

func (s *healthService) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// newHealthClient поднимает сервер с цепочкой интерцепторов и возвращает клиента и сервис-счётчик.
func newHealthClient(
	t *testing.T,
	check checkFunc,
	interceptors ...grpc.UnaryServerInterceptor,
) (grpc_health_v1.HealthClient, *healthService) {
	t.Helper()

	svc := &healthService{check: check}
	conn := grpctest.NewConn(t, func(s *grpc.Server) {
		grpc_health_v1.RegisterHealthServer(s, svc)
	}, grpc.ChainUnaryInterceptor(interceptors...))

	return grpc_health_v1.NewHealthClient(conn), svc
}

func okCheck(
	context.Context,
	*grpc_health_v1.HealthCheckRequest,
) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

func errCode(err error) string {
	return status.Code(err).String()
}
