package middleware

import (
	"net/http"
	"uuid"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
)

const RequestIDHeader = "X-Request-ID"

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = uuid.NewV7().String()
		}
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(appctx.WithRequestID(r.Context(), id)))
	})
}
