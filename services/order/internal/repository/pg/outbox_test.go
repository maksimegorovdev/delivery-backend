//nolint:testpackage // traceContext неэкспортируемая, тест нужен из пакета pg
package pg

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceContext(t *testing.T) {
	t.Parallel()

	traceID, err := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := trace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}

	tracestate, err := trace.ParseTraceState("vendor=value")
	if err != nil {
		t.Fatal(err)
	}

	withSpan := func(state trace.TraceState) context.Context {
		return trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    traceID,
			SpanID:     spanID,
			TraceFlags: trace.FlagsSampled,
			TraceState: state,
			Remote:     true,
		}))
	}

	const wantTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	wantTracestate := "vendor=value"

	tests := []struct {
		name            string
		ctx             context.Context
		wantTraceparent *string
		wantTracestate  *string
	}{
		{
			name:            "span с tracestate",
			ctx:             withSpan(tracestate),
			wantTraceparent: &[]string{wantTraceparent}[0],
			wantTracestate:  &wantTracestate,
		},
		{
			name:            "span без tracestate",
			ctx:             withSpan(trace.TraceState{}),
			wantTraceparent: &[]string{wantTraceparent}[0],
		},
		{
			name: "без span",
			ctx:  context.Background(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotTraceparent, gotTracestate := traceContext(tt.ctx, propagation.TraceContext{})

			assertOptString(t, "traceparent", gotTraceparent, tt.wantTraceparent)
			assertOptString(t, "tracestate", gotTracestate, tt.wantTracestate)
		})
	}
}

func assertOptString(t *testing.T, name string, got, want *string) {
	t.Helper()

	switch {
	case got == nil && want == nil:
	case got == nil || want == nil:
		t.Errorf("%s = %v, want %v", name, deref(got), deref(want))
	case *got != *want:
		t.Errorf("%s = %q, want %q", name, *got, *want)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
