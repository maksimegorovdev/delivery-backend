package httpserver_test

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/http/httpserver"
)

const waitTimeout = 5 * time.Second

// listener — loopback-листенер на порту, выданном ОС, который сообщает о своём закрытии.
// Shutdown закрывает листенер первым шагом, поэтому closed отмечает начало остановки.
type listener struct {
	net.Listener

	once   sync.Once
	closed chan struct{}
}

func listen(t *testing.T) *listener {
	t.Helper()

	var lc net.ListenConfig
	lis, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	l := &listener{Listener: lis, closed: make(chan struct{})}
	t.Cleanup(func() { _ = l.Close() })

	return l
}

func (l *listener) Close() error {
	l.once.Do(func() { close(l.closed) })

	return l.Listener.Close()
}

func (l *listener) url(path string) string {
	return "http://" + l.Addr().String() + path
}

// requireClosed проверяет, что листенер закрыт: Accept на закрытом листенере сразу возвращает [net.ErrClosed].
// Дедлайн не даёт тесту зависнуть, если листенер остался открытым.
func requireClosed(t *testing.T, l *listener) {
	t.Helper()

	if tcp, ok := l.Listener.(*net.TCPListener); ok {
		// На закрытом листенере SetDeadline возвращает ошибку — это и есть ожидаемый случай.
		_ = tcp.SetDeadline(time.Now().Add(waitTimeout))
	}
	_, err := l.Accept()
	require.ErrorIs(t, err, net.ErrClosed, "listener must be closed after Serve returns")
}

// startServe запускает srv.Serve в горутине. Листенер уже слушает, поэтому соединения
// ждут в очереди до Accept и ждать готовности сервера не нужно.
func startServe(ctx context.Context, srv *httpserver.Server, lis net.Listener) <-chan error {
	result := make(chan error, 1)
	go func() { result <- srv.Serve(ctx, lis) }()

	return result
}

type response struct {
	status int
	err    error
}

// get отправляет GET в горутине без keep-alive, чтобы соединение не пережило запрос.
func get(ctx context.Context, url string) <-chan response {
	out := make(chan response, 1)
	go func() { out <- doGet(ctx, url) }()

	return out
}

func doGet(ctx context.Context, url string) response {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return response{err: err}
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return response{err: err}
	}
	_ = resp.Body.Close()

	return response{status: resp.StatusCode}
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

func TestServe_ServesUntilContextCanceled(t *testing.T) {
	t.Parallel()

	lis := listen(t)
	srv := httpserver.New("unused")
	srv.Router().Get("/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := startServe(ctx, srv, lis)

	resp := receive(t, get(ctx, lis.url("/ping")), "response")
	require.NoError(t, resp.err)
	assert.Equal(t, http.StatusNoContent, resp.status)

	cancel()

	require.NoError(t, receive(t, result, "Serve result"))
	requireClosed(t, lis)
}

func TestServe_GracefulShutdownWaitsForActiveRequest(t *testing.T) {
	t.Parallel()

	lis := listen(t)
	srv := httpserver.New("unused")
	entered := make(chan struct{})
	release := make(chan struct{})
	srv.Router().Get("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := startServe(ctx, srv, lis)

	resp := get(t.Context(), lis.url("/slow"))
	receive(t, entered, "handler entry")

	cancel()

	// Shutdown начался с закрытия листенера, а Serve ждёт активный запрос.
	receive(t, lis.closed, "listener close")
	select {
	case err := <-result:
		require.FailNow(t, "Serve returned before the active request finished", "err: %v", err)
	default:
	}

	close(release)

	got := receive(t, resp, "response")
	require.NoError(t, got.err)
	assert.Equal(t, http.StatusNoContent, got.status)
	require.NoError(t, receive(t, result, "Serve result"))
}

func TestServe_GracefulShutdownTimeout(t *testing.T) {
	t.Parallel()

	lis := listen(t)
	srv := httpserver.New("unused", httpserver.WithShutdownTimeout(50*time.Millisecond))
	entered := make(chan struct{})
	release := make(chan struct{})
	srv.Router().Get("/stuck", func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := startServe(ctx, srv, lis)

	resp := get(t.Context(), lis.url("/stuck"))
	receive(t, entered, "handler entry")

	cancel()

	err := receive(t, result, "Serve result")
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "graceful stop timed out after 50ms")

	// Отпускаем зависший хендлер: горутины запроса завершаются до проверки goleak.
	close(release)
	receive(t, resp, "response")
}
