//nolint:testpackage // тест использует неэкспортируемые символы пакета
package postgres

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

const testDSN = "postgres://user:pass@localhost:5432/db"

//nolint:paralleltest // подменяет глобальный TracerProvider OTel, тест нельзя запускать параллельно
func TestNewPoolConfig(t *testing.T) {
	t.Run("sets max conns", func(t *testing.T) {
		cfg, err := newPoolConfig(testDSN, &postgres{maxConns: 7})

		require.NoError(t, err)
		require.EqualValues(t, 7, cfg.MaxConns)
	})

	t.Run("invalid dsn", func(t *testing.T) {
		_, err := newPoolConfig("://bad", &postgres{maxConns: defaultMaxConns})

		require.ErrorContains(t, err, "postgres: parse dsn")
	})

	t.Run("query span is child of caller span with sql text and without args", func(t *testing.T) {
		recorder := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

		prev := otel.GetTracerProvider()
		otel.SetTracerProvider(tp)
		t.Cleanup(func() { otel.SetTracerProvider(prev) })

		cfg, err := newPoolConfig(testDSN, &postgres{maxConns: defaultMaxConns})
		require.NoError(t, err)

		tracer := cfg.ConnConfig.Tracer
		require.NotNil(t, tracer)

		const secret = "s3cr3t-param-value"
		const query = "SELECT id FROM users WHERE email = $1"

		parentCtx, parent := tp.Tracer("test").Start(context.Background(), "grpc.server")
		queryCtx := tracer.TraceQueryStart(parentCtx, nil, pgx.TraceQueryStartData{
			SQL:  query,
			Args: []any{secret},
		})
		tracer.TraceQueryEnd(queryCtx, nil, pgx.TraceQueryEndData{})
		parent.End()

		ended := recorder.Ended()
		require.Len(t, ended, 2)

		querySpan := ended[0]
		require.Equal(t, parent.SpanContext().SpanID(), querySpan.Parent().SpanID())
		require.Equal(t, parent.SpanContext().TraceID(), querySpan.SpanContext().TraceID())

		attrs := map[attribute.Key]attribute.Value{}
		for _, kv := range querySpan.Attributes() {
			attrs[kv.Key] = kv.Value
			require.NotContains(t, kv.Value.String(), secret, "attribute %s leaks query args", kv.Key)
		}
		require.Equal(t, query, attrs["db.query.text"].AsString())
	})
}
