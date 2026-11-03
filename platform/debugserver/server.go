// Package debugserver — HTTP-сервер отладочных эндпоинтов pprof на отдельном порту.
//
// Маршруты повторяют net/http/pprof, но собраны поверх runtime/pprof и runtime/trace на собственном
// [http.ServeMux]: импорт net/http/pprof безусловно регистрирует профили на [http.DefaultServeMux],
// который легко отдать наружу по ошибке.
//
// Сервер не предназначен для публичной сети: слушать его нужно на loopback или во внутренней сети.
package debugserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"time"
)

const (
	defaultReadHeaderTimeout = 5 * time.Second
	// defaultWriteTimeout больше 30 с профиля по умолчанию у `go tool pprof` и 60 с у `task pprof`.
	defaultWriteTimeout    = 65 * time.Second
	defaultIdleTimeout     = 60 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	// maxHeaderBytes: отладочным запросам нужны считаные заголовки.
	maxHeaderBytes = 8 << 10
)

// Server обслуживает /debug/pprof/*.
type Server struct {
	server        *http.Server
	logger        *slog.Logger
	timeouts      Timeouts
	mutexFraction int
	blockRate     int
}

// New собирает сервер. Слушать адрес он начинает в [Server.Serve].
func New(opts ...Option) *Server {
	s := &Server{
		logger: slog.Default(),
		timeouts: Timeouts{
			ReadHeader: defaultReadHeaderTimeout,
			Write:      defaultWriteTimeout,
			Idle:       defaultIdleTimeout,
			Shutdown:   defaultShutdownTimeout,
		},
	}

	for _, opt := range opts {
		opt(s)
	}

	s.server = &http.Server{
		Handler:           newMux(),
		ReadHeaderTimeout: s.timeouts.ReadHeader,
		WriteTimeout:      s.timeouts.Write,
		IdleTimeout:       s.timeouts.Idle,
		MaxHeaderBytes:    maxHeaderBytes,
		ErrorLog:          slog.NewLogLogger(s.logger.Handler(), slog.LevelError),
	}

	return s
}

// Serve обслуживает соединения lis до отмены ctx, затем выполняет graceful shutdown.
// Листенер создаёт вызывающий (обычно через listener.Listen); Serve закрывает lis.
//
// Доли mutex- и block-профилей включаются на время работы и сбрасываются при выходе.
// Если lis слушает не loopback, Serve предупреждает об этом в лог: у эндпоинтов нет аутентификации.
//
// Контексты запросов производны от ctx: при его отмене идущий CPU-профиль или трассировка
// завершаются досрочно, и клиент получает данные за прошедшее время. Иначе [http.Server.Shutdown]
// ждал бы конца seconds, а постоянный скрейп (Alloy) превращал бы каждую остановку в таймаут.
// Если запросы всё же не завершились за Shutdown (например, клиент не читает ответ),
// соединения закрываются принудительно, и Serve возвращает ошибку.
func (s *Server) Serve(ctx context.Context, lis net.Listener) error {
	defer s.applyProfileRates()()

	if addr, ok := lis.Addr().(*net.TCPAddr); ok && !addr.IP.IsLoopback() {
		s.logger.WarnContext(ctx, "debugserver: listening on a non-loopback address, pprof has no authentication",
			slog.String("addr", addr.String()))
	}

	s.server.BaseContext = func(net.Listener) context.Context { return ctx }

	serveErr := make(chan error, 1)
	go func() {
		if err := s.server.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("debugserver: serve: %w", err)
	case <-ctx.Done():
		return s.shutdown()
	}
}

func (s *Server) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeouts.Shutdown)
	defer cancel()

	err := s.server.Shutdown(ctx)
	if err == nil {
		return nil
	}

	// Запрос не завершился даже после отмены его контекста: соединение закрывается принудительно.
	if cerr := s.server.Close(); cerr != nil {
		s.logger.Warn("debugserver: force close", slog.Any("error", cerr))
	}

	return fmt.Errorf("debugserver: graceful stop timed out after %s: %w", s.timeouts.Shutdown, err)
}

// applyProfileRates включает заданные доли профилей и возвращает функцию, которая их сбрасывает.
func (s *Server) applyProfileRates() func() {
	prevMutex := -1
	if s.mutexFraction > 0 {
		prevMutex = runtime.SetMutexProfileFraction(s.mutexFraction)
	}
	if s.blockRate > 0 {
		runtime.SetBlockProfileRate(s.blockRate)
	}

	return func() {
		if prevMutex >= 0 {
			runtime.SetMutexProfileFraction(prevMutex)
		}
		if s.blockRate > 0 {
			runtime.SetBlockProfileRate(0)
		}
	}
}
