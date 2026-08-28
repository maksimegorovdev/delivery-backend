package logger

import (
	"io"
	"log/slog"
	"strings"
)

type Option func(*config)

func WithWriter(w io.Writer) Option {
	return func(c *config) {
		c.writer = w
	}
}

func WithLevel(level string) Option {
	return func(c *config) {
		switch strings.ToLower(level) {
		case "debug":
			c.level = slog.LevelDebug
		case "warn":
			c.level = slog.LevelWarn
		case "error":
			c.level = slog.LevelError
		default:
			c.level = slog.LevelInfo
		}
	}
}

func WithFormat(format string) Option {
	return func(c *config) {
		switch strings.ToLower(format) {
		case "json":
			c.format = FormatJSON
		default:
			c.format = FormatText
		}
	}
}
