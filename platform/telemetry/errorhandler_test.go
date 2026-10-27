//nolint:testpackage // тест использует неэкспортируемые символы пакета
package telemetry

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
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
	if len(lines) != 1 {
		t.Fatalf("после первой ошибки строк лога = %d, want 1: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "level=WARN") || !strings.Contains(lines[0], "connection refused") {
		t.Errorf("первая строка не WARN с ошибкой: %s", lines[0])
	}

	// Ошибки внутри интервала подавляются.
	for range 3 {
		now = now.Add(5 * time.Second)
		h.Handle(exportErr)
	}
	if got := logLines(buf); len(got) != 1 {
		t.Fatalf("внутри интервала строк лога = %d, want 1: %q", len(got), got)
	}

	// После интервала — один WARN с числом пропущенных.
	now = now.Add(30 * time.Second)
	h.Handle(exportErr)
	lines = logLines(buf)
	if len(lines) != 2 {
		t.Fatalf("после интервала строк лога = %d, want 2: %q", len(lines), lines)
	}
	if !strings.Contains(lines[1], "suppressed=3") {
		t.Errorf("вторая строка без suppressed=3: %s", lines[1])
	}

	// Счётчик сброшен: следующая ошибка снова подавляется.
	h.Handle(exportErr)
	if got := logLines(buf); len(got) != 2 {
		t.Errorf("сразу после WARN строк лога = %d, want 2", len(got))
	}
}

//nolint:paralleltest // подменяет slog.Default, тест нельзя запускать параллельно
func TestErrorHandler_UsesCurrentDefaultLogger(t *testing.T) {
	h := newErrorHandler(30*time.Second, time.Now)

	// Логгер, установленный после создания handler'а, должен подхватываться.
	buf := captureDefaultLogger(t)
	h.Handle(errors.New("boom"))

	if lines := logLines(buf); len(lines) != 1 {
		t.Errorf("строк лога = %d, want 1", len(lines))
	}
}
