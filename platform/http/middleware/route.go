package middleware

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Route после роутинга переносит шаблон маршрута chi в server-спан (имя «METHOD route»
// и атрибут http.route) и в метрики otelhttp. Шаблон, а не сырой путь, держит
// кардинальность под контролем. Должен быть первым в цепочке chi.
//
// otelhttp сам этого не делает: chi передаёт дальше копию запроса, и r.Pattern
// исходного запроса остаётся пустым.
func Route(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)

		rctx := chi.RouteContext(r.Context())
		if rctx == nil {
			return
		}
		pattern := rctx.RoutePattern()
		if pattern == "" {
			return
		}

		route := attribute.String("http.route", pattern)

		span := trace.SpanFromContext(r.Context())
		span.SetName(r.Method + " " + pattern)
		span.SetAttributes(route)

		if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
			labeler.Add(route)
		}
	})
}
