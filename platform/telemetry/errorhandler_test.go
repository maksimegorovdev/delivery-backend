//nolint:testpackage // тест использует неэкспортируемые символы пакета
package telemetry

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureDefaultLogger подменяет [slog.Default] на текстовый логгер в буфере.
func captureDefaultLogger(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func logLines(buf *bytes.Buffer) []string {
	s := strings.TrimSpace(buf.String())
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

//nolint:paralleltest // подменяет slog.Default, тест нельзя запускать параллельно
func TestErrorHandler_Throttling(t *testing.T) {
	buf := captureDefaultLogger(t)

	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	h := newErrorHandler(30*time.Second, func() time.Time { return now })
	exportErr := errors.New("connection refused")

	// Первая ошибка логируется сразу.
	h.Handle(exportErr)
	lines := logLines(buf)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "level=WARN")
	assert.Contains(t, lines[0], "connection refused")

	// Ошибки внутри интервала подавляются.
	for range 3 {
		now = now.Add(5 * time.Second)
		h.Handle(exportErr)
	}
	require.Len(t, logLines(buf), 1)

	// После интервала — один WARN с числом пропущенных.
	now = now.Add(30 * time.Second)
	h.Handle(exportErr)
	lines = logLines(buf)
	require.Len(t, lines, 2)
	assert.Contains(t, lines[1], "suppressed=3")

	// Счётчик сброшен: следующая ошибка снова подавляется.
	h.Handle(exportErr)
	assert.Len(t, logLines(buf), 2)
}

//nolint:paralleltest // подменяет slog.Default, тест нельзя запускать параллельно
func TestErrorHandler_UsesCurrentDefaultLogger(t *testing.T) {
	h := newErrorHandler(30*time.Second, time.Now)

	// Логгер, установленный после создания handler'а, должен подхватываться.
	buf := captureDefaultLogger(t)
	h.Handle(errors.New("boom"))

	assert.Len(t, logLines(buf), 1)
}
