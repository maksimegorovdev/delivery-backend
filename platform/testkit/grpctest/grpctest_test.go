package grpctest_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/grpctest"
)

func registerHealth(srv *grpc.Server) {
	hs := health.NewServer()
	hs.SetServingStatus("known", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
}

func TestNewConn(t *testing.T) {
	t.Parallel()

	t.Run("call reaches registered server", func(t *testing.T) {
		t.Parallel()

		conn := grpctest.NewConn(t, registerHealth)

		resp, err := healthpb.NewHealthClient(conn).Check(t.Context(),
			&healthpb.HealthCheckRequest{Service: "known"})

		require.NoError(t, err)
		require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.GetStatus())
	})

	t.Run("server error is returned to client", func(t *testing.T) {
		t.Parallel()

		conn := grpctest.NewConn(t, registerHealth)

		_, err := healthpb.NewHealthClient(conn).Check(t.Context(),
			&healthpb.HealthCheckRequest{Service: "unknown"})

		require.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("server options are applied", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32
		counter := func(
			ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
		) (any, error) {
			calls.Add(1)
			return handler(ctx, req)
		}

		conn := grpctest.NewConn(t, registerHealth, grpc.UnaryInterceptor(counter))

		_, err := healthpb.NewHealthClient(conn).Check(t.Context(),
			&healthpb.HealthCheckRequest{Service: "known"})

		require.NoError(t, err)
		require.EqualValues(t, 1, calls.Load())
	})
}
