package debugserver_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"runtime/trace"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/debugserver"
)

const waitTimeout = 10 * time.Second

// gzipMagic — начало gzip-потока: бинарные профили pprof (debug=0) сжаты gzip.
var gzipMagic = []byte{0x1f, 0x8b} //nolint:gochecknoglobals // неизменяемая константа теста

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

// start запускает сервер на новом листенере и останавливает его по завершении теста.
func start(t *testing.T, opts ...debugserver.Option) *listener {
	t.Helper()

	lis := listen(t)
	srv := debugserver.New(opts...)

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- srv.Serve(ctx, lis) }()

	t.Cleanup(func() {
		cancel()
		select {
		case <-result:
		case <-time.After(waitTimeout):
		}
	})

	return lis
}

type response struct {
	status int
	body   []byte
	err    error
}

// do отправляет запрос без keep-alive, чтобы соединение не пережило запрос.
func do(ctx context.Context, method, url string, body io.Reader) response {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return response{err: err}
	}
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		return response{err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)

	return response{status: resp.StatusCode, body: data, err: err}
}

func get(t *testing.T, url string) response {
	t.Helper()

	resp := do(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, resp.err)

	return resp
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

func TestServer_IndexListsProfiles(t *testing.T) {
	t.Parallel()

	lis := start(t)

	resp := get(t, lis.url("/debug/pprof/"))

	assert.Equal(t, http.StatusOK, resp.status)
	for _, name := range []string{"goroutine", "heap", "allocs", "goroutineleak", "mutex", "block", "threadcreate"} {
		assert.Contains(t, string(resp.body), name)
	}
}

func TestServer_NamedProfiles(t *testing.T) {
	t.Parallel()

	lis := start(t)

	tests := []struct {
		name     string
		path     string
		wantText string // для debug=1 — читаемый вывод; пусто — бинарный gzip
	}{
		{name: "goroutine text", path: "/debug/pprof/goroutine?debug=1", wantText: "goroutine profile:"},
		{name: "goroutine binary", path: "/debug/pprof/goroutine"},
		{name: "heap binary", path: "/debug/pprof/heap"},
		{name: "heap with gc", path: "/debug/pprof/heap?gc=1"},
		{name: "allocs binary", path: "/debug/pprof/allocs"},
		// Профиль goroutineleak появился в Go 1.27; pprof.Lookup отдаёт его без отдельного хендлера.
		{name: "goroutineleak binary", path: "/debug/pprof/goroutineleak"},
		{name: "mutex text", path: "/debug/pprof/mutex?debug=1", wantText: "mutex"},
		{name: "block text", path: "/debug/pprof/block?debug=1", wantText: "contention"},
		{name: "threadcreate text", path: "/debug/pprof/threadcreate?debug=1", wantText: "threadcreate profile:"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := get(t, lis.url(tt.path))

			require.Equal(t, http.StatusOK, resp.status)
			require.NotEmpty(t, resp.body)
			if tt.wantText != "" {
				assert.Contains(t, string(resp.body), tt.wantText)
				return
			}
			assert.True(t, bytes.HasPrefix(resp.body, gzipMagic), "бинарный профиль должен быть gzip")
		})
	}
}

func TestServer_InvalidDebug(t *testing.T) {
	t.Parallel()

	lis := start(t)

	for _, value := range []string{"-1", "abc", "1.5"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			resp := get(t, lis.url("/debug/pprof/goroutine?debug="+value))

			assert.Equal(t, http.StatusBadRequest, resp.status)
			assert.Contains(t, string(resp.body), "invalid debug")
		})
	}
}

// gc=1 запускает сборку мусора только перед heap: у остальных профилей она лишь тормозит процесс.
// Автоматическая сборка выключена, чтобы NumGC менялся только от запроса; GOGC глобален, тест не параллельный.
//
//nolint:paralleltest // GOGC глобален для процесса
func TestServer_GCOnlyForHeap(t *testing.T) {
	lis := start(t)

	prevGCPercent := debug.SetGCPercent(-1)
	t.Cleanup(func() { debug.SetGCPercent(prevGCPercent) })

	tests := []struct {
		name   string
		path   string
		wantGC bool
	}{
		{name: "heap", path: "/debug/pprof/heap?gc=1", wantGC: true},
		{name: "allocs", path: "/debug/pprof/allocs?gc=1", wantGC: false},
		{name: "goroutine", path: "/debug/pprof/goroutine?gc=1", wantGC: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := numGC()

			require.Equal(t, http.StatusOK, get(t, lis.url(tt.path)).status)

			assert.Equal(t, tt.wantGC, numGC() > before, "сборка мусора перед профилем")
		})
	}
}

func numGC() uint32 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	return ms.NumGC
}

func TestServer_Cmdline(t *testing.T) {
	t.Parallel()

	lis := start(t)

	resp := get(t, lis.url("/debug/pprof/cmdline"))

	assert.Equal(t, http.StatusOK, resp.status)
	assert.NotEmpty(t, resp.body)
}

func TestServer_Symbol(t *testing.T) {
	t.Parallel()

	lis := start(t)

	t.Run("GET reports symbol table presence", func(t *testing.T) {
		t.Parallel()

		resp := get(t, lis.url("/debug/pprof/symbol"))

		assert.Equal(t, http.StatusOK, resp.status)
		assert.Contains(t, string(resp.body), "num_symbols: 1")
	})

	t.Run("POST resolves addresses to function names", func(t *testing.T) {
		t.Parallel()

		pc := reflect.ValueOf(TestServer_Symbol).Pointer()
		body := strings.NewReader(fmt.Sprintf("%#x", pc))

		resp := do(t.Context(), http.MethodPost, lis.url("/debug/pprof/symbol"), body)

		require.NoError(t, resp.err)
		assert.Equal(t, http.StatusOK, resp.status)
		assert.Contains(t, string(resp.body), "TestServer_Symbol")
	})
}

func TestServer_NotFound(t *testing.T) {
	t.Parallel()

	lis := start(t)

	for _, path := range []string{
		"/",
		"/debug/",
		"/debug/vars",
		"/metrics",
		"/debug/pprof/unknown",
		"/debug/pprof/heap/extra",
	} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, http.StatusNotFound, get(t, lis.url(path)).status)
		})
	}
}

// Профили не должны попадать в [http.DefaultServeMux]: его обычно отдают наружу по ошибке.
func TestServer_NotOnDefaultServeMux(t *testing.T) {
	t.Parallel()

	// Сервер создаётся, чтобы регистрация (если она есть) уже произошла.
	start(t)

	other := httptest.NewServer(http.DefaultServeMux)
	t.Cleanup(other.Close)

	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/profile"} {
		assert.Equal(t, http.StatusNotFound, get(t, other.URL+path).status, path)
	}
}

// Профилирование CPU и трассировка глобальны для процесса, поэтому тесты ниже не параллельные:
// два одновременных сеанса дали бы 500 «already in use».

//nolint:paralleltest // профилирование глобально для процесса
func TestServer_CPUProfile(t *testing.T) {
	lis := start(t)

	resp := get(t, lis.url("/debug/pprof/profile?seconds=1"))

	require.Equal(t, http.StatusOK, resp.status)
	assert.True(t, bytes.HasPrefix(resp.body, gzipMagic), "CPU-профиль должен быть gzip")
}

//nolint:paralleltest // профилирование глобально для процесса
func TestServer_Trace(t *testing.T) {
	lis := start(t)

	resp := get(t, lis.url("/debug/pprof/trace?seconds=1"))

	require.Equal(t, http.StatusOK, resp.status)
	assert.NotEmpty(t, resp.body)
}

func TestServer_SecondsExceedsWriteTimeout(t *testing.T) {
	t.Parallel()

	lis := start(t, debugserver.WithTimeouts(debugserver.Timeouts{Write: 2 * time.Second}))

	// seconds, равный WriteTimeout, тоже отклоняется: на запись ответа после сна времени не остаётся.
	for _, path := range []string{
		"/debug/pprof/profile?seconds=5",
		"/debug/pprof/trace?seconds=5",
		"/debug/pprof/profile?seconds=2",
		"/debug/pprof/trace?seconds=2",
	} {
		resp := get(t, lis.url(path))

		assert.Equal(t, http.StatusBadRequest, resp.status, path)
		assert.Contains(t, string(resp.body), "WriteTimeout", path)
	}
}

//nolint:paralleltest // профилирование глобально для процесса
func TestServer_ProfileAlreadyRunning(t *testing.T) {
	lis := start(t)

	// CPU-профиль занят снаружи, как при параллельном запросе другого клиента.
	require.NoError(t, pprof.StartCPUProfile(io.Discard))
	defer pprof.StopCPUProfile()

	resp := get(t, lis.url("/debug/pprof/profile?seconds=1"))

	assert.Equal(t, http.StatusInternalServerError, resp.status)
	assert.Contains(t, string(resp.body), "could not enable CPU profiling")
}

func TestServe_StopsOnContextCancel(t *testing.T) {
	t.Parallel()

	lis := listen(t)
	srv := debugserver.New()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- srv.Serve(ctx, lis) }()

	require.Equal(t, http.StatusOK, get(t, lis.url("/debug/pprof/")).status)

	cancel()

	require.NoError(t, receive(t, result, "Serve result"))
	receive(t, lis.closed, "listener close")
	assert.Error(t, do(t.Context(), http.MethodGet, lis.url("/debug/pprof/"), nil).err, "порт больше не слушается")
}

//nolint:paralleltest // профилирование глобально для процесса
func TestServe_ShutdownTimeoutAbortsRunningProfile(t *testing.T) {
	lis := listen(t)
	srv := debugserver.New(debugserver.WithTimeouts(debugserver.Timeouts{Shutdown: 50 * time.Millisecond}))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- srv.Serve(ctx, lis) }()

	inFlight := make(chan response, 1)
	go func() { inFlight <- do(t.Context(), http.MethodGet, lis.url("/debug/pprof/trace?seconds=30"), nil) }()

	require.Eventually(t, trace.IsEnabled, waitTimeout, 10*time.Millisecond, "трассировка не стартовала")

	cancel()

	err := receive(t, result, "Serve result")
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "graceful stop timed out after 50ms")

	// Принудительное закрытие прерывает хендлер, иначе трассировка держалась бы 30 с.
	receive(t, inFlight, "aborted request")
}

//nolint:paralleltest // профилирование глобально для процесса
func TestServe_AppliesAndRestoresMutexFraction(t *testing.T) {
	before := runtime.SetMutexProfileFraction(-1)

	lis := listen(t)
	srv := debugserver.New(debugserver.WithMutexProfileFraction(7))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- srv.Serve(ctx, lis) }()

	require.Equal(t, http.StatusOK, get(t, lis.url("/debug/pprof/")).status)
	assert.Equal(t, 7, runtime.SetMutexProfileFraction(-1), "доля mutex-профиля включена на время работы сервера")

	cancel()
	require.NoError(t, receive(t, result, "Serve result"))

	assert.Equal(t, before, runtime.SetMutexProfileFraction(-1), "прежняя доля восстановлена")
}

// Получить текущий block rate из runtime нельзя, поэтому он проверяется по поведению:
// блокировка в blockBriefly попадает в профиль, пока сервер работает, и не попадает после его остановки.
//
//nolint:paralleltest // профилирование глобально для процесса
func TestServe_AppliesAndResetsBlockProfileRate(t *testing.T) {
	lis := listen(t)
	srv := debugserver.New(debugserver.WithBlockProfileRate(1))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- srv.Serve(ctx, lis) }()

	require.Equal(t, http.StatusOK, get(t, lis.url("/debug/pprof/")).status)

	before := blockEvents(t)
	blockBriefly()
	assert.Greater(t, blockEvents(t), before, "block-профиль включён на время работы сервера")

	cancel()
	require.NoError(t, receive(t, result, "Serve result"))

	before = blockEvents(t)
	blockBriefly()
	assert.Equal(t, before, blockEvents(t), "block-профиль выключен после остановки сервера")
}

// blockBriefly блокируется на канале примерно на миллисекунду.
//
//go:noinline // функция ищется по имени в стеке block-профиля
func blockBriefly() {
	ch := make(chan struct{})
	go func() {
		time.Sleep(time.Millisecond)
		close(ch)
	}()
	<-ch
}

// blockEvents возвращает число событий блокировки в block-профиле, у которых в стеке есть blockBriefly.
func blockEvents(t *testing.T) int64 {
	t.Helper()

	n, _ := runtime.BlockProfile(nil)
	records := make([]runtime.BlockProfileRecord, n+16)
	n, ok := runtime.BlockProfile(records)
	require.True(t, ok, "буфер block-профиля мал")

	var events int64
	for _, rec := range records[:n] {
		frames := runtime.CallersFrames(rec.Stack())
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, ".blockBriefly") {
				events += rec.Count
				break
			}
			if !more {
				break
			}
		}
	}

	return events
}
