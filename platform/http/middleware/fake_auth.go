package middleware

import (
	"net/http"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
)

const UserIDHeader = "X-User-ID"

func FakeAuth(defaultUserID string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			userID := r.Header.Get(UserIDHeader)
			if userID == "" {
				userID = defaultUserID
			}
			next.ServeHTTP(w, r.WithContext(appctx.WithUserID(r.Context(), userID)))
		})
	}
}
