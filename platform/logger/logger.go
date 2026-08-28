package logger

import (
	"io"
	"log/slog"
	"os"
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

type config struct {
	writer io.Writer
	level  slog.Level
	format Format
}

func New(opts ...Option) *slog.Logger {
	cfg := &config{
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
	switch cfg.format {
	case FormatJSON:
		handler = slog.NewJSONHandler(cfg.writer, handlerOpts)
	default:
		handler = slog.NewTextHandler(cfg.writer, handlerOpts)
	}

	return slog.New(NewCtxHandler(handler))
}
