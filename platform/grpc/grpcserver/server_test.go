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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

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

// runServer запускает Serve на lis и возвращает клиента и канал результата Serve.
func runServer(
	ctx context.Context,
	t *testing.T,
	lis net.Listener,
	opts ...grpcserver.Option,
) (grpc_health_v1.HealthClient, <-chan error) {
	t.Helper()

	srv := grpcserver.New(opts...)

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

// blockingServiceDesc — тестовый сервис с одним стримом, который не завершается до остановки сервера.
//
//nolint:gochecknoglobals // неизменяемое описание тестового сервиса
var blockingServiceDesc = grpc.ServiceDesc{
	ServiceName: "test.Blocking",
	HandlerType: (*any)(nil),
	Streams: []grpc.StreamDesc{{
		StreamName:    "Hold",
		ServerStreams: true,
		Handler: func(_ any, stream grpc.ServerStream) error {
			// Заголовки сообщают клиенту, что обработка началась.
			if err := stream.SendHeader(nil); err != nil {
				return err
			}

			<-stream.Context().Done()

			return stream.Context().Err()
		},
	}},
}

func TestServe_GracefulStopTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	lis := listen(t)
	srv := grpcserver.New(grpcserver.WithShutdownTimeout(50 * time.Millisecond))
	srv.Server().RegisterService(&blockingServiceDesc, struct{}{})

	result := make(chan error, 1)
	go func() { result <- srv.Serve(ctx, lis) }()

	conn, err := grpcclient.New(lis.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, check(ctx, grpc_health_v1.NewHealthClient(conn)))

	// Активный стрим не даёт GracefulStop завершиться, поэтому срабатывает таймаут.
	streamCtx, stopStream := context.WithCancel(t.Context())
	defer stopStream()
	stream, err := conn.NewStream(
		streamCtx,
		&blockingServiceDesc.Streams[0],
		"/test.Blocking/Hold",
		grpc.WaitForReady(true),
	)
	require.NoError(t, err)
	require.NoError(t, stream.CloseSend())
	// Заголовки приходят, когда сервер начал обрабатывать стрим.
	_, err = stream.Header()
	require.NoError(t, err)

	cancel()

	require.ErrorContains(t, receive(t, result, "Serve result"), "grpcserver: graceful stop timed out")
}

func TestServe_ReportsServing(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	client, result := runServer(ctx, t, listen(t))

	resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{}, grpc.WaitForReady(true))
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.GetStatus())

	cancel()
	require.NoError(t, receive(t, result, "Serve result"))
}

func TestServe_WatchEndsOnShutdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		service   string
		wantFirst grpc_health_v1.HealthCheckResponse_ServingStatus
	}{
		{name: "overall status", service: "", wantFirst: grpc_health_v1.HealthCheckResponse_SERVING},
		{
			name:      "unknown service",
			service:   "unknown.Service",
			wantFirst: grpc_health_v1.HealthCheckResponse_SERVICE_UNKNOWN,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			// Таймаут больше waitTimeout: если Watch держит GracefulStop, receive упадёт раньше таймаута.
			client, result := runServer(ctx, t, listen(t), grpcserver.WithShutdownTimeout(2*waitTimeout))
			require.NoError(t, check(ctx, client))

			watchCtx, stopWatch := context.WithCancel(t.Context())
			defer stopWatch()
			stream, err := client.Watch(watchCtx, &grpc_health_v1.HealthCheckRequest{Service: tt.service})
			require.NoError(t, err)
			first, err := stream.Recv()
			require.NoError(t, err)
			require.Equal(t, tt.wantFirst, first.GetStatus())

			cancel()

			next, err := stream.Recv()
			require.NoError(t, err)
			assert.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, next.GetStatus())

			_, err = stream.Recv()
			assert.Equal(t, codes.Unavailable, status.Code(err), "stream must end after NOT_SERVING: %v", err)

			// Watch не держит GracefulStop: Serve завершается без таймаута.
			require.NoError(t, receive(t, result, "Serve result"))
		})
	}
}
