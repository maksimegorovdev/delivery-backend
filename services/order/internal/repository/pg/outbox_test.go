//nolint:testpackage // traceContext неэкспортируемая, тест нужен из пакета pg
package pg

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceContext(t *testing.T) {
	t.Parallel()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	require.NoError(t, err)

	tracestate, err := trace.ParseTraceState("vendor=value")
	require.NoError(t, err)

	withSpan := func(state trace.TraceState) context.Context {
		return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
			TraceState: state,
			Remote:     true,
		}))
	}

	const (
		wantTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		wantTracestate  = "vendor=value"
	)

	tests := []struct {
		name            string
		ctx             context.Context
		wantTraceparent *string
		wantTracestate  *string
	}{
		{
			name:            "span with tracestate",
			ctx:             withSpan(tracestate),
			wantTraceparent: new(wantTraceparent),
			wantTracestate:  new(wantTracestate),
		},
		{
			name:            "span without tracestate",
			ctx:             withSpan(trace.TraceState{}),
			wantTraceparent: new(wantTraceparent),
		},
		{
			name: "no span",
			ctx:  context.Background(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotTraceparent, gotTracestate := traceContext(tt.ctx, propagation.TraceContext{})

			assert.Equal(t, tt.wantTraceparent, gotTraceparent)
			assert.Equal(t, tt.wantTracestate, gotTracestate)
		})
	}
}
