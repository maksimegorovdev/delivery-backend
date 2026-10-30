package middleware_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
)

func TestRecover(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		panic any
	}{
		{"string panic", "boom"},
		{"error panic", errors.New("boom")},
		{"non-error value panic", 42},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := middleware.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic(tc.panic)
			}))
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()

			require.NotPanics(t, func() { handler.ServeHTTP(rec, req) })

			assert.Equal(t, http.StatusInternalServerError, rec.Code)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, "INTERNAL", body.Error.Code)
		})
	}
}

func TestRecover_StoresPanicErrorInErrBox(t *testing.T) {
	t.Parallel()

	handler := middleware.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}))
	ctx := appctx.WithErrBox(t.Context())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil)

	handler.ServeHTTP(httptest.NewRecorder(), req)

	err := appctx.GetErr(ctx)
	require.Error(t, err)
	assert.ErrorContains(t, err, "panic: boom")
}

func TestRecover_ServesNextRequestAfterPanic(t *testing.T) {
	t.Parallel()

	handler := middleware.Recover(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panic" {
			panic("boom")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	panicRec := httptest.NewRecorder()
	handler.ServeHTTP(panicRec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/panic", nil))
	okRec := httptest.NewRecorder()
	handler.ServeHTTP(okRec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ok", nil))

	assert.Equal(t, http.StatusInternalServerError, panicRec.Code)
	assert.Equal(t, http.StatusNoContent, okRec.Code)
}

func TestRecover_PassesThroughWithoutPanic(t *testing.T) {
	t.Parallel()

	handler := middleware.Recover(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

	assert.Equal(t, http.StatusTeapot, rec.Code)
}
