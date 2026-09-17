package middleware

import (
	"fmt"
	"net/http"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/http/response"
)

func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				response.Fail(w, r, apperr.Internal().Wrap(fmt.Errorf("panic: %v", err)))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
