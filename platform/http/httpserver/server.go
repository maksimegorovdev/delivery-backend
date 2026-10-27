package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	defaultShutdownTimeout = 10 * time.Second
	// defaultReadHeaderTimeout защищает от Slowloris-атак.
	defaultReadHeaderTimeout = 10 * time.Second
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

	for _, opt := range opts {
		opt(srv)
	}

	srv.server = &http.Server{
		Handler:           srv.router,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
	}

	return srv
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
