package logger_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/maksimegorovdev/delivery-backend/platform/logger"
)

func TestDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    time.Duration
		expected float64
	}{
		{name: "sub-millisecond keeps fraction", input: 592334 * time.Nanosecond, expected: 0.592334},
		{name: "whole milliseconds", input: 1500 * time.Millisecond, expected: 1500},
		{name: "zero", input: 0, expected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			attr := logger.Duration(tt.input)

			assert.Equal(t, "duration_ms", attr.Key)
			// Float64, а не Duration: otelslog и JSONHandler пишут Duration целыми наносекундами.
			assert.Equal(t, slog.KindFloat64, attr.Value.Kind())
			assert.InDelta(t, tt.expected, attr.Value.Float64(), 1e-9)
		})
	}
}
