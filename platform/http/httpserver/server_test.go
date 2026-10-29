package httpserver_test

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/http/httpserver"
)

const waitTimeout = 5 * time.Second

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

// canDial сообщает, принимает ли addr новые соединения.
func canDial(ctx context.Context, addr string) bool {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()

	return true
}

// start запускает srv.Run в горутине и ждёт, пока сервер начнёт принимать соединения.
func start(ctx context.Context, t *testing.T, srv *httpserver.Server, addr string) <-chan error {
	t.Helper()

	result := make(chan error, 1)
	go func() { result <- srv.Run(ctx) }()

	require.Eventually(t, func() bool { return canDial(ctx, addr) }, waitTimeout, time.Millisecond)

	return result
}

type response struct {
	status int
	err    error
}

// get отправляет GET в горутине без keep-alive, чтобы соединение не пережило запрос.
func get(ctx context.Context, url string) <-chan response {
	out := make(chan response, 1)
	go func() {
		client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			out <- response{err: err}
			return
		}
		resp, err := client.Do(req)
		if err != nil {
			out <- response{err: err}
			return
		}
		_ = resp.Body.Close()
		out <- response{status: resp.StatusCode}
	}()

	return out
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

func TestServer_Router(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, httpserver.New("127.0.0.1:0").Router())
}

func TestRun_ServesUntilContextCanceled(t *testing.T) {
	t.Parallel()

	addr := freeAddr(t)
	srv := httpserver.New(addr)
	srv.Router().Get("/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := start(ctx, t, srv, addr)

	resp := receive(t, get(ctx, "http://"+addr+"/ping"), "response")
	require.NoError(t, resp.err)
	assert.Equal(t, http.StatusNoContent, resp.status)

	cancel()

	require.NoError(t, receive(t, result, "Run result"))
	assert.False(t, canDial(t.Context(), addr), "listener must be closed after Run returns")
}

func TestRun_ListenError(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = busy.Close() })

	srv := httpserver.New(busy.Addr().String())

	err = srv.Run(t.Context())

	require.Error(t, err)
	assert.ErrorContains(t, err, "httpserver: listen")
}

func TestRun_GracefulShutdownWaitsForActiveRequest(t *testing.T) {
	t.Parallel()

	addr := freeAddr(t)
	srv := httpserver.New(addr)
	entered := make(chan struct{})
	release := make(chan struct{})
	srv.Router().Get("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := start(ctx, t, srv, addr)

	resp := get(t.Context(), "http://"+addr+"/slow")
	receive(t, entered, "handler entry")

	cancel()

	// Listener закрывается в начале Shutdown: новые соединения отклоняются, а Run ждёт активный запрос.
	require.Eventually(t, func() bool { return !canDial(t.Context(), addr) }, waitTimeout, time.Millisecond)
	select {
	case err := <-result:
		require.FailNow(t, "Run returned before the active request finished", "err: %v", err)
	default:
	}

	close(release)

	got := receive(t, resp, "response")
	require.NoError(t, got.err)
	assert.Equal(t, http.StatusNoContent, got.status)
	require.NoError(t, receive(t, result, "Run result"))
}

func TestRun_GracefulShutdownTimeout(t *testing.T) {
	t.Parallel()

	addr := freeAddr(t)
	srv := httpserver.New(addr, httpserver.WithShutdownTimeout(50*time.Millisecond))
	entered := make(chan struct{})
	release := make(chan struct{})
	srv.Router().Get("/stuck", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := start(ctx, t, srv, addr)

	resp := get(t.Context(), "http://"+addr+"/stuck")
	receive(t, entered, "handler entry")

	cancel()

	err := receive(t, result, "Run result")
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "graceful stop timed out after 50ms")

	// Отпускаем зависший хендлер: горутины запроса завершаются до проверки goleak.
	close(release)
	receive(t, resp, "response")
}
