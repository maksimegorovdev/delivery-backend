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

const (
	reflectionService = "grpc.reflection.v1.ServerReflection"
	waitTimeout       = 5 * time.Second
)

// listen открывает loopback-листенер на порту, выданном ОС.
func listen(t *testing.T) net.Listener {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis.Close() })

	return lis
}

// requireClosed проверяет, что листенер закрыт: Accept на закрытом листенере сразу возвращает [net.ErrClosed].
// Дедлайн не даёт тесту зависнуть, если листенер остался открытым.
func requireClosed(t *testing.T, lis net.Listener) {
	t.Helper()

	if tcp, ok := lis.(*net.TCPListener); ok {
		// На закрытом листенере SetDeadline возвращает ошибку — это и есть ожидаемый случай.
		_ = tcp.SetDeadline(time.Now().Add(waitTimeout))
	}
	_, err := lis.Accept()
	require.ErrorIs(t, err, net.ErrClosed, "listener must be closed after Serve returns")
}

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(waitTimeout):
		require.FailNow(t, "timed out waiting for "+what)
	}

	var zero T

	return zero
}

// runServer регистрирует health-сервис, запускает Serve на lis и возвращает клиента и канал результата Serve.
func runServer(
	ctx context.Context,
	t *testing.T,
	lis net.Listener,
	opts ...grpcserver.Option,
) (grpc_health_v1.HealthClient, <-chan error) {
	t.Helper()

	srv := grpcserver.New(opts...)
	grpc_health_v1.RegisterHealthServer(srv.Server(), health.NewServer())

	result := make(chan error, 1)
	go func() { result <- srv.Serve(ctx, lis) }()

	conn, err := grpcclient.New(lis.Addr().String())
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

			srv := grpcserver.New(tt.opts...)

			require.NotNil(t, srv.Server())
			assert.Equal(t, tt.want, hasService(srv.Server(), reflectionService))
		})
	}
}

func hasService(srv *grpc.Server, name string) bool {
	_, ok := srv.GetServiceInfo()[name]

	return ok
}

func TestServe_ServesUntilContextCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	lis := listen(t)
	client, result := runServer(ctx, t, lis)
	require.NoError(t, check(ctx, client))

	cancel()

	require.NoError(t, receive(t, result, "Serve result"))
	requireClosed(t, lis)
}

func TestServe_AppliesServerOptions(t *testing.T) {
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

	client, result := runServer(ctx, t, listen(t), grpcserver.WithServerOptions(grpc.UnaryInterceptor(counter)))
	require.NoError(t, check(ctx, client))
	assert.EqualValues(t, 1, calls.Load())

	cancel()
	require.NoError(t, receive(t, result, "Serve result"))
}

func TestServe_GracefulStopTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client, result := runServer(ctx, t, listen(t), grpcserver.WithShutdownTimeout(50*time.Millisecond))
	require.NoError(t, check(ctx, client))

	// Watch держит активный стрим, поэтому GracefulStop не завершается и срабатывает таймаут.
	watchCtx, stopWatch := context.WithCancel(t.Context())
	defer stopWatch()
	stream, err := client.Watch(watchCtx, &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.NoError(t, err)

	cancel()

	require.ErrorContains(t, receive(t, result, "Serve result"), "grpcserver: graceful stop timed out")
}
