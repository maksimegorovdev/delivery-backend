package middleware

import (
	"net/http"
	"uuid"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
)

const RequestIDHeader = "X-Request-ID"

// maxRequestIDLen ограничивает длину входящего request ID: значение попадает в ответ,
// логи и спаны, поэтому произвольная строка от клиента не принимается.
const maxRequestIDLen = 128

// RequestID берёт request ID из заголовка или генерирует UUIDv7, если заголовок пуст
// или не проходит [isValidRequestID].
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !isValidRequestID(id) {
			id = uuid.NewV7().String()
		}
		w.Header().Set(RequestIDHeader, id)
		trace.SpanFromContext(r.Context()).SetAttributes(attribute.String("request.id", id))
		next.ServeHTTP(w, r.WithContext(appctx.WithRequestID(r.Context(), id)))
	})
}

// isValidRequestID допускает непустую строку не длиннее maxRequestIDLen
// из символов [A-Za-z0-9_-].
func isValidRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for _, c := range []byte(id) {
		isAlnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !isAlnum && c != '-' && c != '_' {
			return false
		}
	}
	return true
}
