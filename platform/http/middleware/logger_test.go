package middleware_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
	"github.com/maksimegorovdev/delivery-backend/platform/http/response"
)

func serveLogged(t *testing.T, method, path string, next http.HandlerFunc) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := middleware.Logger(log)(next)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), method, path, nil))

	var record map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record), "exactly one JSON log line expected")
	return record
}

func TestLogger_SuccessfulRequest(t *testing.T) {
	t.Parallel()

	record := serveLogged(t, http.MethodPost, "/orders", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	assert.Equal(t, "INFO", record["level"])
	assert.Equal(t, "http request handled", record["msg"])
	assert.NotContains(t, record, "error")

	httpGroup, ok := record["http"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, http.MethodPost, httpGroup["method"])
	assert.Equal(t, "/orders", httpGroup["path"])
	assert.InDelta(t, http.StatusCreated, httpGroup["status_code"], 0)
	assert.Contains(t, httpGroup, "duration_ms")
}

func TestLogger_ImplicitStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		next       http.HandlerFunc
		wantStatus int
	}{
		{
			"body write implies OK",
			func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) },
			http.StatusOK,
		},
		// Фиксирует текущее поведение chi-обёртки: net/http отдаст клиенту 200,
		// но в лог попадёт 0, пока handler не вызвал Write или WriteHeader.
		{"empty response logs zero status", func(http.ResponseWriter, *http.Request) {}, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			record := serveLogged(t, http.MethodGet, "/", tc.next)

			httpGroup, ok := record["http"].(map[string]any)
			require.True(t, ok)
			assert.InDelta(t, tc.wantStatus, httpGroup["status_code"], 0)
		})
	}
}

func TestLogger_FailedRequestLevelFollowsErrorCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantLevel  string
		wantStatus int
	}{
		{"client error is warning", apperr.NotFound(), "WARN", http.StatusNotFound},
		{"server error is error", apperr.Internal(), "ERROR", http.StatusInternalServerError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			record := serveLogged(t, http.MethodGet, "/orders/1", func(w http.ResponseWriter, r *http.Request) {
				response.Fail(w, r, tc.err)
			})

			assert.Equal(t, tc.wantLevel, record["level"])
			assert.Equal(t, "http request failed", record["msg"])
			assert.Contains(t, record, "error")

			httpGroup, ok := record["http"].(map[string]any)
			require.True(t, ok)
			assert.InDelta(t, tc.wantStatus, httpGroup["status_code"], 0)
		})
	}
}

func TestLogger_LogsPanicRecoveredByInnerMiddleware(t *testing.T) {
	t.Parallel()

	record := serveLogged(
		t,
		http.MethodGet,
		"/",
		middleware.Recover(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("boom")
		})).ServeHTTP,
	)

	assert.Equal(t, "ERROR", record["level"])
	assert.Equal(t, "http request failed", record["msg"])
}
