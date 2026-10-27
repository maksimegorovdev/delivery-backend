package telemetry

import (
	"log/slog"
	"sync"
	"time"
)

// errorLogInterval — минимальный интервал между WARN об ошибках экспорта.
const errorLogInterval = 30 * time.Second

// errorHandler реализует otel.ErrorHandler: пишет ошибки SDK в slog.Default()
// не чаще одного раза за interval, а число пропущенных ошибок добавляет к следующему WARN.
// Логгер берётся при каждом вызове, поскольку создаётся уже после telemetry.New.
type errorHandler struct {
	interval time.Duration
	now      func() time.Time

	mu         sync.Mutex
	last       time.Time
	suppressed int
}

func newErrorHandler(interval time.Duration, now func() time.Time) *errorHandler {
	return &errorHandler{interval: interval, now: now}
}

// Handle подавляет ошибку или логирует её вместе со счётчиком подавленных.
func (h *errorHandler) Handle(err error) {
	h.mu.Lock()
	t := h.now()
	if !h.last.IsZero() && t.Sub(h.last) < h.interval {
		h.suppressed++
		h.mu.Unlock()
		return
	}
	suppressed := h.suppressed
	h.last = t
	h.suppressed = 0
	h.mu.Unlock()

	slog.Warn("telemetry: export error", "error", err, "suppressed", suppressed)
}
