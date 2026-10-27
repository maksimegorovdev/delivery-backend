package logger

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/log"
)

const otelScopeName = "github.com/maksimegorovdev/delivery-backend/platform/logger"

// otelHandler адаптирует slog к OTel-логам:
//   - отсекает записи ниже level (otelslog пропускает все уровни, и без этого LOG_LEVEL
//     не действовал бы на экспорт);
//   - раскладывает группы в плоские ключи "group.key" вместо вложенной map,
//     чтобы по атрибутам можно было фильтровать в ClickHouse.
type otelHandler struct {
	level   slog.Leveler
	handler slog.Handler
	prefix  string
}

func newOTelHandler(provider log.LoggerProvider, level slog.Leveler) *otelHandler {
	return &otelHandler{
		level:   level,
		handler: otelslog.NewHandler(otelScopeName, otelslog.WithLoggerProvider(provider)),
	}
}

func (h *otelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.level.Level() && h.handler.Enabled(ctx, l)
}

func (h *otelHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(flattenAttr(h.prefix, a)...)

		return true
	})

	return h.handler.Handle(ctx, out)
}

func (h *otelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	flat := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		flat = append(flat, flattenAttr(h.prefix, a)...)
	}

	return &otelHandler{level: h.level, handler: h.handler.WithAttrs(flat), prefix: h.prefix}
}

func (h *otelHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return &otelHandler{level: h.level, handler: h.handler, prefix: h.prefix + name + "."}
}

// flattenAttr разворачивает группы рекурсивно; пустые группы отбрасываются, как в slog.
func flattenAttr(prefix string, a slog.Attr) []slog.Attr {
	v := a.Value.Resolve()
	if v.Kind() != slog.KindGroup {
		return []slog.Attr{{Key: prefix + a.Key, Value: v}}
	}

	groupPrefix := prefix
	if a.Key != "" {
		groupPrefix += a.Key + "."
	}

	var out []slog.Attr
	for _, ga := range v.Group() {
		out = append(out, flattenAttr(groupPrefix, ga)...)
	}

	return out
}
