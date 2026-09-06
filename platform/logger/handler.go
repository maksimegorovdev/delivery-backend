package logger

import (
	"context"
	"log/slog"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
)

type CtxHandler struct {
	handler slog.Handler
}

func NewCtxHandler(h slog.Handler) *CtxHandler {
	return &CtxHandler{handler: h}
}

func (h *CtxHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.handler.Enabled(ctx, l)
}

func (h *CtxHandler) Handle(ctx context.Context, r slog.Record) error {
	if reqID := appctx.GetRequestID(ctx); reqID != "" {
		r.AddAttrs(slog.String("request_id", reqID))
	}

	return h.handler.Handle(ctx, r)
}

func (h *CtxHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &CtxHandler{
		handler: h.handler.WithAttrs(attrs),
	}
}

func (h *CtxHandler) WithGroup(name string) slog.Handler {
	return &CtxHandler{
		handler: h.handler.WithGroup(name),
	}
}
