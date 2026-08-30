package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	defaultHost            = ""
	defaultPort            = 8080
	defaultShutdownTimeout = 10 * time.Second
)

type Server struct {
	server          *http.Server
	router          *chi.Mux
	host            string
	port            int
	shutdownTimeout time.Duration
}

func New(opts ...Option) *Server {
	srv := &Server{
		router:          chi.NewRouter(),
		host:            defaultHost,
		port:            defaultPort,
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
	lis, err := net.Listen("tcp", net.JoinHostPort(s.host, strconv.Itoa(s.port)))
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
