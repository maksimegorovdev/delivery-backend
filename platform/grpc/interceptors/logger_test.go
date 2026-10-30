package interceptors_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
)

// syncBuffer — буфер для slog-хендлера, в который пишет серверная горутина, а читает тест.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	return bytes.Clone(b.buf.Bytes())
}

func newJSONLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}

	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

func decodeRecord(t *testing.T, buf *syncBuffer) map[string]any {
	t.Helper()

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	return rec
}

func TestLogger_LogsSuccessfulRequestAtInfo(t *testing.T) {
	t.Parallel()

	log, buf := newJSONLogger()
	client, _ := newHealthClient(t, okCheck, interceptors.Logger(log))

	_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)

	rec := decodeRecord(t, buf)
	assert.Equal(t, "grpc request handled", rec["msg"])
	assert.Equal(t, "INFO", rec["level"])

	grpcAttrs, ok := rec["grpc"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, codes.OK.String(), grpcAttrs["code"])
	assert.Equal(t, grpc_health_v1.Health_Check_FullMethodName, grpcAttrs["method"])
}

func TestLogger_LogsFailureWithCodeAndLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       *apperr.Error
		wantLevel string
		wantCode  string
	}{
		{name: "client error is warn", err: apperr.NotFound(), wantLevel: "WARN", wantCode: codes.NotFound.String()},
		{name: "server error is error", err: apperr.Internal(), wantLevel: "ERROR", wantCode: codes.Internal.String()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log, buf := newJSONLogger()
			client, _ := newHealthClient(t, failWith(tt.err), interceptors.Logger(log))

			_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})
			require.Error(t, err)

			rec := decodeRecord(t, buf)
			assert.Equal(t, "grpc request failed", rec["msg"])
			assert.Equal(t, tt.wantLevel, rec["level"])

			grpcAttrs, ok := rec["grpc"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tt.wantCode, grpcAttrs["code"])
			assert.Equal(t, grpc_health_v1.Health_Check_FullMethodName, grpcAttrs["method"])
			assert.Contains(t, rec, "error")
		})
	}
}

func TestLogger_DoesNotChangeHandlerError(t *testing.T) {
	t.Parallel()

	log, _ := newJSONLogger()
	client, _ := newHealthClient(t, failWith(apperr.NotFound()), interceptors.Error(), interceptors.Logger(log))

	_, err := client.Check(t.Context(), &grpc_health_v1.HealthCheckRequest{})

	require.Error(t, err)
	assert.Equal(t, codes.NotFound.String(), errCode(err))
}
