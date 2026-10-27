package logger

import (
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/log"
)

type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

const (
	defaultLevel  = slog.LevelInfo
	defaultFormat = FormatText
)

type logger struct {
	writer io.Writer
	level  slog.Level
	format Format

	// provider включает экспорт записей в OTel; nil — только stdout.
	provider log.LoggerProvider
}

func New(opts ...Option) *slog.Logger {
	cfg := &logger{
		writer: os.Stdout,
		level:  defaultLevel,
		format: defaultFormat,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	handlerOpts := &slog.HandlerOptions{
		Level: cfg.level,
	}

	var handler slog.Handler
	//nolint:exhaustive // остальные форматы обрабатывает default (текстовый)
	switch cfg.format {
	case FormatJSON:
		handler = slog.NewJSONHandler(cfg.writer, handlerOpts)
	default:
		handler = slog.NewTextHandler(cfg.writer, handlerOpts)
	}

	handler = newTraceHandler(handler)
	if cfg.provider != nil {
		handler = slog.NewMultiHandler(handler, newOTelHandler(cfg.provider, cfg.level))
	}

	return slog.New(NewCtxHandler(handler))
}
