package grpcserver_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcclient"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcserver"
)

const reflectionService = "grpc.reflection.v1.ServerReflection"

// freeAddr резервирует свободный loopback-порт: Run принимает адрес строкой и не отдаёт выбранный порт.
func freeAddr(t *testing.T) string {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	return addr
}

// runServer регистрирует health-сервис, запускает Run и возвращает клиента и канал результата Run.
func runServer(
	ctx context.Context,
	t *testing.T,
	addr string,
	opts ...grpcserver.Option,
) (grpc_health_v1.HealthClient, <-chan error) {
	t.Helper()

	srv := grpcserver.New(addr, opts...)
	grpc_health_v1.RegisterHealthServer(srv.Server(), health.NewServer())

	result := make(chan error, 1)
	go func() { result <- srv.Run(ctx) }()

	conn, err := grpcclient.New(addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return grpc_health_v1.NewHealthClient(conn), result
}

func check(ctx context.Context, client grpc_health_v1.HealthClient) error {
	_, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{}, grpc.WaitForReady(true))

	return err
}

func TestNew_Reflection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []grpcserver.Option
		want bool
	}{
		{name: "disabled by default", want: false},
		{name: "enabled by option", opts: []grpcserver.Option{grpcserver.WithReflection(true)}, want: true},
		{name: "explicitly disabled", opts: []grpcserver.Option{grpcserver.WithReflection(false)}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := grpcserver.New("127.0.0.1:0", tt.opts...)

			require.NotNil(t, srv.Server())
			assert.Equal(t, tt.want, hasService(srv.Server(), reflectionService))
		})
	}
}

func hasService(srv *grpc.Server, name string) bool {
	_, ok := srv.GetServiceInfo()[name]

	return ok
}

func TestRun_ServesUntilContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client, result := runServer(ctx, t, freeAddr(t))
	require.NoError(t, check(ctx, client))

	cancel()

	select {
	case err := <-result:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		require.Fail(t, "Run did not return after context cancellation")
	}
}

func TestRun_AppliesServerOptions(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	counter := func(
		ctx context.Context,
		req any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		calls.Add(1)

		return handler(ctx, req)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client, result := runServer(ctx, t, freeAddr(t), grpcserver.WithServerOptions(grpc.UnaryInterceptor(counter)))
	require.NoError(t, check(ctx, client))
	assert.EqualValues(t, 1, calls.Load())

	cancel()
	require.NoError(t, <-result)
}

func TestRun_ListenError(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = busy.Close() })

	srv := grpcserver.New(busy.Addr().String())

	err = srv.Run(t.Context())

	require.Error(t, err)
	assert.ErrorContains(t, err, "grpcserver: listen")
}

func TestRun_GracefulStopTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client, result := runServer(ctx, t, freeAddr(t), grpcserver.WithShutdownTimeout(50*time.Millisecond))
	require.NoError(t, check(ctx, client))

	// Watch держит активный стрим, поэтому GracefulStop не завершается и срабатывает таймаут.
	watchCtx, stopWatch := context.WithCancel(t.Context())
	defer stopWatch()
	stream, err := client.Watch(watchCtx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)

	cancel()

	select {
	case runErr := <-result:
		require.ErrorContains(t, runErr, "grpcserver: graceful stop timed out")
	case <-time.After(5 * time.Second):
		require.Fail(t, "Run did not return after shutdown timeout")
	}
}
