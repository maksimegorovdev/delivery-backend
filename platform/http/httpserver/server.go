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
		Handler: srv.router,
	}

	return srv
}

func (s *Server) Router() *chi.Mux {
	return s.router
}

func (s *Server) Run(ctx context.Context) error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("httpserver: listen: %w", err)
	}

	serveErr := make(chan error, 1)
	go func() {
		if err := s.server.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("httpserver: serve: %w", err)
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
