package logger

import (
	"context"
	"log/slog"
	"slices"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
)

// CtxHandler добавляет в запись request_id из ctx.
//
// Группы из WithGroup хендлер держит у себя и раскладывает во вложенные атрибуты
// в Handle, а не передаёт во внутренний хендлер. Иначе request_id, trace_id и span_id,
// которые добавляются в Handle, попали бы внутрь группы ("http.trace_id=...").
type CtxHandler struct {
	handler slog.Handler
	groups  []group
}

// group — группа из WithGroup и атрибуты, добавленные в неё через WithAttrs.
type group struct {
	name  string
	attrs []slog.Attr
}

func NewCtxHandler(h slog.Handler) *CtxHandler {
	return &CtxHandler{handler: h}
}

func (h *CtxHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.handler.Enabled(ctx, l)
}

func (h *CtxHandler) Handle(ctx context.Context, r slog.Record) error {
	if len(h.groups) > 0 {
		r = h.nest(r)
	}

	if reqID := appctx.GetRequestID(ctx); reqID != "" {
		r.AddAttrs(slog.String("request_id", reqID))
	}

	return h.handler.Handle(ctx, r)
}

// nest переносит атрибуты записи внутрь накопленных групп, от внутренней к внешней.
// Пустые группы отбросит внутренний хендлер, как и для обычного WithGroup.
func (h *CtxHandler) nest(r slog.Record) slog.Record {
	inner := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		inner = append(inner, a)

		return true
	})

	for _, g := range slices.Backward(h.groups) {
		attrs := append(slices.Clone(g.attrs), inner...)
		inner = []slog.Attr{{Key: g.name, Value: slog.GroupValue(attrs...)}}
	}

	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	out.AddAttrs(inner...)

	return out
}

func (h *CtxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	if len(h.groups) == 0 {
		return &CtxHandler{handler: h.handler.WithAttrs(attrs)}
	}

	groups := slices.Clone(h.groups)
	last := &groups[len(groups)-1]
	last.attrs = append(slices.Clone(last.attrs), attrs...)

	return &CtxHandler{handler: h.handler, groups: groups}
}

func (h *CtxHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	return &CtxHandler{
		handler: h.handler,
		groups:  append(slices.Clone(h.groups), group{name: name}),
	}
}
