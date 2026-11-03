package debugserver

import (
	"log/slog"
	"time"
)

// Option настраивает [Server].
type Option func(*Server)

// Timeouts — таймауты сервера. Нулевое поле оставляет значение по умолчанию.
type Timeouts struct {
	// ReadHeader ограничивает чтение заголовков запроса.
	ReadHeader time.Duration
	// Write ограничивает ответ целиком; параметр seconds профиля CPU и трассировки не может его превышать.
	Write time.Duration
	// Idle ограничивает простой keep-alive соединения.
	Idle time.Duration
	// Shutdown ограничивает graceful-остановку; по его истечении соединения закрываются принудительно.
	Shutdown time.Duration
}

// WithMutexProfileFraction включает mutex-профиль на время работы сервера: в профиль попадает
// в среднем одно из n событий конкуренции за mutex. Значение n <= 0 оставляет профиль выключенным.
func WithMutexProfileFraction(n int) Option {
	return func(s *Server) {
		s.mutexFraction = n
	}
}

// WithBlockProfileRate включает block-профиль на время работы сервера: в профиль попадает
// одно событие блокировки на каждые rate наносекунд ожидания. Значение rate <= 0 оставляет профиль выключенным.
func WithBlockProfileRate(rate int) Option {
	return func(s *Server) {
		s.blockRate = rate
	}
}

// WithLogger задаёт логгер ошибок http-сервера и остановки. По умолчанию — [slog.Default].
func WithLogger(l *slog.Logger) Option {
	return func(s *Server) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithTimeouts переопределяет таймауты сервера.
func WithTimeouts(t Timeouts) Option {
	return func(s *Server) {
		if t.ReadHeader > 0 {
			s.timeouts.ReadHeader = t.ReadHeader
		}
		if t.Write > 0 {
			s.timeouts.Write = t.Write
		}
		if t.Idle > 0 {
			s.timeouts.Idle = t.Idle
		}
		if t.Shutdown > 0 {
			s.timeouts.Shutdown = t.Shutdown
		}
	}
}
