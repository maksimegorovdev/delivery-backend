package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
)

func TestFakeAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		defaultUserID string
		header        string
		want          string
	}{
		{"header overrides default", "default-user", "user-42", "user-42"},
		{"missing header falls back to default", "default-user", "", "default-user"},
		{"empty default and no header", "", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got string
			handler := middleware.FakeAuth(
				tc.defaultUserID,
			)(
				http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					got = appctx.GetUserID(r.Context())
				}),
			)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set(middleware.UserIDHeader, tc.header)
			}
			handler.ServeHTTP(httptest.NewRecorder(), req)

			assert.Equal(t, tc.want, got)
		})
	}
}
