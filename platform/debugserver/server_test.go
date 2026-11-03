package debugserver_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
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
		{name: "heap gc=0", path: "/debug/pprof/heap?gc=0", wantGC: false},
		{name: "heap gc not a number", path: "/debug/pprof/heap?gc=yes", wantGC: false},
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

// Пустой, нечисловой и неположительный seconds заменяется значением по умолчанию (30 с у профиля,
// 1 с у трассировки). Подстановку видно по отказу: значение по умолчанию не укладывается в WriteTimeout,
// тогда как переданное как есть (0, -1) уложилось бы и запустило профилирование.
func TestServer_SecondsDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		path  string
		write time.Duration
	}{
		{name: "profile without seconds", path: "/debug/pprof/profile", write: 2 * time.Second},
		{name: "profile empty", path: "/debug/pprof/profile?seconds=", write: 2 * time.Second},
		{name: "profile zero", path: "/debug/pprof/profile?seconds=0", write: 2 * time.Second},
		{name: "profile negative", path: "/debug/pprof/profile?seconds=-1", write: 2 * time.Second},
		{name: "profile not a number", path: "/debug/pprof/profile?seconds=abc", write: 2 * time.Second},
		{
			name:  "profile overflows int",
			path:  "/debug/pprof/profile?seconds=99999999999999999999",
			write: 2 * time.Second,
		},
		{name: "trace without seconds", path: "/debug/pprof/trace", write: time.Second},
		{name: "trace zero", path: "/debug/pprof/trace?seconds=0", write: time.Second},
		{name: "trace not a number", path: "/debug/pprof/trace?seconds=abc", write: time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lis := start(t, debugserver.WithTimeouts(debugserver.Timeouts{Write: tt.write}))

			resp := get(t, lis.url(tt.path))

			assert.Equal(t, http.StatusBadRequest, resp.status)
			assert.Contains(t, string(resp.body), "WriteTimeout")
		})
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

// Клиент, оборвавший запрос, освобождает трассировку сразу, а не через seconds:
// иначе она занимала бы процесс и другие клиенты получали бы 500.
//
//nolint:paralleltest // профилирование глобально для процесса
func TestServer_ClientDisconnectStopsTrace(t *testing.T) {
	lis := start(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	inFlight := make(chan response, 1)
	go func() { inFlight <- do(ctx, http.MethodGet, lis.url("/debug/pprof/trace?seconds=30"), nil) }()

	require.Eventually(t, trace.IsEnabled, waitTimeout, 10*time.Millisecond, "трассировка не стартовала")

	cancel()

	require.ErrorIs(t, receive(t, inFlight, "aborted request").err, context.Canceled)
	assert.Eventually(t, func() bool { return !trace.IsEnabled() }, waitTimeout, 10*time.Millisecond,
		"трассировка остановлена после отключения клиента")
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

// publicAddrListener сообщает не-loopback адрес, оставаясь на loopback: тест не открывает порт наружу.
type publicAddrListener struct {
	net.Listener
}

func (publicAddrListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4zero, Port: 6060}
}

// На не-loopback адресе Serve предупреждает, что pprof доступен без аутентификации; на loopback молчит.
func TestServe_WarnsOnNonLoopbackAddr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wrap     func(net.Listener) net.Listener
		wantWarn bool
	}{
		{
			name:     "loopback",
			wrap:     func(l net.Listener) net.Listener { return l },
			wantWarn: false,
		},
		{
			name:     "all interfaces",
			wrap:     func(l net.Listener) net.Listener { return publicAddrListener{l} },
			wantWarn: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer
			srv := debugserver.New(debugserver.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))

			lis := tt.wrap(listen(t))

			ctx, cancel := context.WithCancel(t.Context())
			result := make(chan error, 1)
			go func() { result <- srv.Serve(ctx, lis) }()

			cancel()
			require.NoError(t, receive(t, result, "Serve result"))

			// Запись в logs произошла в Serve до отправки результата в канал.
			if tt.wantWarn {
				assert.Contains(t, logs.String(), "non-loopback address")
				assert.Contains(t, logs.String(), "0.0.0.0:6060")
				return
			}
			assert.NotContains(t, logs.String(), "non-loopback")
		})
	}
}

// Ошибка Accept завершает Serve без отмены ctx, а доли профилей всё равно восстанавливаются.
//
//nolint:paralleltest // доля mutex-профиля глобальна для процесса
func TestServe_ReturnsAcceptError(t *testing.T) {
	before := runtime.SetMutexProfileFraction(-1)

	lis := listen(t)
	require.NoError(t, lis.Close())
	srv := debugserver.New(debugserver.WithMutexProfileFraction(7))

	result := make(chan error, 1)
	go func() { result <- srv.Serve(t.Context(), lis) }()

	err := receive(t, result, "Serve result")
	require.ErrorIs(t, err, net.ErrClosed)
	require.ErrorContains(t, err, "debugserver: serve:")
	assert.Equal(t, before, runtime.SetMutexProfileFraction(-1), "прежняя доля восстановлена")
}

// Остановка не ждёт конца seconds: идущая трассировка завершается досрочно, клиент получает
// снятые данные, а Serve завершается без ошибки. Иначе постоянный скрейп превращал бы каждую
// остановку сервиса в таймаут.
//
//nolint:paralleltest // профилирование глобально для процесса
func TestServe_StopEndsRunningTrace(t *testing.T) {
	lis := listen(t)
	srv := debugserver.New()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- srv.Serve(ctx, lis) }()

	inFlight := make(chan response, 1)
	go func() { inFlight <- do(t.Context(), http.MethodGet, lis.url("/debug/pprof/trace?seconds=30"), nil) }()

	require.Eventually(t, trace.IsEnabled, waitTimeout, 10*time.Millisecond, "трассировка не стартовала")

	cancel()

	// waitTimeout (10 с) меньше seconds=30: Serve дождался бы конца трассировки только с ошибкой теста.
	require.NoError(t, receive(t, result, "Serve result"))

	resp := receive(t, inFlight, "trace response")
	require.NoError(t, resp.err)
	assert.Equal(t, http.StatusOK, resp.status)
	assert.NotEmpty(t, resp.body, "клиент получает трассировку за прошедшее время")
	assert.False(t, trace.IsEnabled(), "трассировка остановлена")
}

// Хендлер, не реагирующий на отмену контекста (здесь — чтение тела POST /symbol, которое клиент
// не досылает), не должен держать остановку бесконечно: по истечении Shutdown соединение закрывается.
func TestServe_ShutdownTimeoutForcesClose(t *testing.T) {
	t.Parallel()

	lis := listen(t)
	srv := debugserver.New(debugserver.WithTimeouts(debugserver.Timeouts{Shutdown: 50 * time.Millisecond}))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- srv.Serve(ctx, lis) }()

	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "tcp", lis.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.SetDeadline(time.Now().Add(waitTimeout)))

	// 100 Continue сервер шлёт, когда хендлер начинает читать тело: запрос точно активен.
	_, err = io.WriteString(conn, "POST /debug/pprof/symbol HTTP/1.1\r\nHost: test\r\n"+
		"Content-Length: 16\r\nExpect: 100-continue\r\n\r\n")
	require.NoError(t, err)
	status := make([]byte, len("HTTP/1.1 100"))
	_, err = io.ReadFull(conn, status)
	require.NoError(t, err)
	require.Equal(t, "HTTP/1.1 100", string(status))

	cancel()

	err = receive(t, result, "Serve result")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "graceful stop timed out after 50ms")

	// Соединение закрыто сервером, а не истёк дедлайн клиента.
	_, err = io.ReadAll(conn)
	assert.NotErrorIs(t, err, os.ErrDeadlineExceeded)
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
