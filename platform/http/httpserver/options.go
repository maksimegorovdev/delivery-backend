package httpserver

import "time"

type Option func(*Server)

func WithShutdownTimeout(timeout time.Duration) Option {
	return func(s *Server) {
		s.shutdownTimeout = timeout
	}
}

// WithPublicEndpoint помечает сервер внешней границей: входящий traceparent
// не становится родителем, а прикрепляется к новому корневому спану ссылкой,
// baggage клиента не принимается. Так клиент не навязывает trace-id и решение
// о сэмплировании и не протаскивает baggage во внутренние сервисы.
func WithPublicEndpoint() Option {
	return func(s *Server) {
		s.publicEndpoint = true
	}
}
