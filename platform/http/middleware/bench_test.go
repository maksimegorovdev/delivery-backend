package middleware_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
	"github.com/maksimegorovdev/delivery-backend/platform/http/response"
)

// discardWriter отбрасывает тело ответа: в бенчмарке измеряется цепочка, а не [httptest.ResponseRecorder].
type discardWriter struct {
	header http.Header
}

func (w *discardWriter) Header() http.Header       { return w.header }
func (*discardWriter) Write(p []byte) (int, error) { return len(p), nil }
func (*discardWriter) WriteHeader(int)             {}
func newDiscardWriter() *discardWriter             { return &discardWriter{header: make(http.Header)} }

// nopWriter — приёмник логов: JSON форматируется как в бою, но не пишется (slog.DiscardHandler пропустил бы форматирование).
type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }

func benchLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(nopWriter{}, nil)) }

// benchRouter собирает ту же цепочку, что и сервисы: Route → RequestID → FakeAuth → Logger → Recover.
func benchRouter(handler http.HandlerFunc) http.Handler {
	r := chi.NewRouter()
	r.Use(
		middleware.Route,
		middleware.RequestID,
		middleware.FakeAuth("01a0d1fa-a4e8-75a1-802d-d6e3c642b0f3"),
		middleware.Logger(benchLogger()),
		middleware.Recover,
	)
	r.Post("/orders", handler)
	return r
}

func BenchmarkMiddleware_Chain(b *testing.B) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"success", func(w http.ResponseWriter, r *http.Request) {
			response.Created(w, r, map[string]string{"id": "1"})
		}},
		{"failure", func(w http.ResponseWriter, r *http.Request) {
			response.Fail(w, r, apperr.NotFound())
		}},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			handler := benchRouter(tc.handler)
			req := httptest.NewRequestWithContext(b.Context(), http.MethodPost, "/orders", nil)
			req.Header.Set(middleware.RequestIDHeader, "bench-request-id")
			w := newDiscardWriter()

			b.ReportAllocs()
			for b.Loop() {
				handler.ServeHTTP(w, req)
			}
		})
	}
}

func BenchmarkRequestID_Generate(b *testing.B) {
	handler := middleware.RequestID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/", nil)
	w := newDiscardWriter()

	b.ReportAllocs()
	for b.Loop() {
		handler.ServeHTTP(w, req)
	}
}
