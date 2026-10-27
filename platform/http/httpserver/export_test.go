package httpserver

import "net/http"

// Handler отдаёт собранный хендлер сервера (с трассировкой) внешним тестам.
func (s *Server) Handler() http.Handler {
	return s.server.Handler
}
