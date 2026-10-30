package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"

	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
)

const (
	defaultShutdownTimeout = 10 * time.Second
	// defaultReadHeaderTimeout защищает от Slowloris-атак.
	defaultReadHeaderTimeout = 10 * time.Second
	// defaultMaxHeaderBytes ограничивает заголовки запроса: их значения попадают в спаны и логи.
	defaultMaxHeaderBytes = 16 << 10
	// swaggerPrefix — документация API, её запросы не трассируются.
	swaggerPrefix = "/swagger/"
	// otelOperation — имя оператора otelhttp; имя спана затем заменяется на «METHOD route».
	otelOperation = "http.server"
)

type Server struct {
	server          *http.Server
	router          *chi.Mux
	addr            string
	shutdownTimeout time.Duration
	publicEndpoint  bool
}

func New(addr string, opts ...Option) *Server {
	srv := &Server{
		router:          chi.NewRouter(),
		addr:            addr,
		shutdownTimeout: defaultShutdownTimeout,
	}

	srv.router.Use(middleware.Route)

	for _, opt := range opts {
		opt(srv)
	}

	srv.server = &http.Server{
		Handler:           srv.tracedHandler(),
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		MaxHeaderBytes:    defaultMaxHeaderBytes,
	}

	return srv
}

// tracedHandler оборачивает роутер в otelhttp: server-спан и метрики на каждый запрос.
// Шаблон маршрута в спан и метрики переносит middleware.Route.
//
// server.address и server.port берутся из адреса прослушивания: без WithServerName
// otelhttp читает их из заголовка Host, и клиент плодит серии метрик.
func (s *Server) tracedHandler() http.Handler {
	opts := []otelhttp.Option{
		otelhttp.WithServerName(s.addr),
		otelhttp.WithFilter(func(r *http.Request) bool {
			return !strings.HasPrefix(r.URL.Path, swaggerPrefix)
		}),
	}
	if !s.publicEndpoint {
		return otelhttp.NewHandler(s.router, otelOperation, opts...)
	}

	opts = append(opts,
		otelhttp.WithPublicEndpointFn(func(*http.Request) bool { return true }),
		otelhttp.WithPropagators(propagation.TraceContext{}),
	)
	return dropUntrustedHeaders(otelhttp.NewHandler(s.router, otelOperation, opts...))
}

// untrustedHeaders — заголовки внешнего клиента, которые otelhttp перенёс бы в телеметрию:
// X-Forwarded-For — в client.address (клиент подставляет чужой IP), tracestate — в link
// корневого спана. Доверенного прокси перед сервером нет, поэтому client.address берётся
// из адреса соединения; если прокси появится, X-Forwarded-For надо разбирать от него.
//
//nolint:gochecknoglobals // неизменяемый список заголовков
var untrustedHeaders = []string{"X-Forwarded-For", "Tracestate"}

// dropUntrustedHeaders удаляет untrustedHeaders из запроса до otelhttp.
func dropUntrustedHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, h := range untrustedHeaders {
			r.Header.Del(h)
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Router() *chi.Mux {
	return s.router
}

func (s *Server) Run(ctx context.Context) error {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", s.addr)
	if err != nil {
		return fmt.Errorf("httpserver: listen: %w", err)
	}

	return s.Serve(ctx, lis)
}

// Serve обслуживает соединения lis до отмены ctx, затем выполняет graceful shutdown.
// Serve закрывает lis. Run — это Serve на листенере по адресу из New; Serve нужен,
// когда листенер создаёт вызывающий (например, тест на порту, выданном ОС).
func (s *Server) Serve(ctx context.Context, lis net.Listener) error {
	serveErr := make(chan error, 1)
	go func() {
		if serr := s.server.Serve(lis); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			serveErr <- serr
		}
	}()

	select {
	case serr := <-serveErr:
		return fmt.Errorf("httpserver: serve: %w", serr)
	case <-ctx.Done():
		return s.shutdown()
	}
}

func (s *Server) shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
	defer cancel()

	if err := s.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("httpserver: graceful stop timed out after %s: %w", s.shutdownTimeout, err)
	}

	return nil
}
