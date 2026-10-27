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

	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
)

const (
	defaultShutdownTimeout = 10 * time.Second
	// defaultReadHeaderTimeout защищает от Slowloris-атак.
	defaultReadHeaderTimeout = 10 * time.Second
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
	}

	return srv
}

// tracedHandler оборачивает роутер в otelhttp: server-спан и метрики на каждый запрос.
// Шаблон маршрута в спан и метрики переносит middleware.Route.
func (s *Server) tracedHandler() http.Handler {
	return otelhttp.NewHandler(s.router, otelOperation,
		otelhttp.WithFilter(func(r *http.Request) bool {
			return !strings.HasPrefix(r.URL.Path, swaggerPrefix)
		}),
	)
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
