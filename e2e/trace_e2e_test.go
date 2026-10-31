//go:build e2e

package e2e_test

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrace_EndToEnd(t *testing.T) {
	t.Parallel()

	// Сервисы, спаны которых должны оказаться в одном трейсе заказа.
	services := []string{"gateway", "order", "user", "product", "notification"}

	clientTraceID := randomHex(t, 16)
	clientSpanID := randomHex(t, 8)

	header := http.Header{}
	header.Set("Traceparent", "00-"+clientTraceID+"-"+clientSpanID+"-01")

	order := createOrderWithHeader(t, createOrderRequest{
		AddressID: seed.AddressID,
		Items:     []orderItem{{ProductID: seed.Borjomi.ID, Quantity: 1}},
	}, header)

	// Заказ дошёл до notification: спан обработки события уже записан или скоро будет.
	awaitOrderEvent(t, order.ID)

	// gateway — публичная граница: клиентский trace_id не становится родителем, а попадает в Links.
	var rootTraceID string

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		roots, err := querySpans(t.Context(), `
			SELECT TraceId, ParentSpanId, ServiceName
			FROM otel.otel_traces
			WHERE ServiceName = 'gateway'
			  AND ParentSpanId = ''
			  AND has(Links.TraceId, {trace_id:String})`,
			map[string]string{"trace_id": clientTraceID})
		if !assert.NoError(c, err) || !assert.Len(c, roots, 1, "gateway root span with client link") {
			return
		}

		rootTraceID = roots[0].TraceID
	}, traceTimeout, eventuallyTick)

	require.NotEqual(t, clientTraceID, rootTraceID, "gateway must start a new trace")

	// Под trace_id корневого спана есть спаны всех пяти сервисов, notification — не корень.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		spans, err := querySpans(t.Context(), `
			SELECT TraceId, ParentSpanId, ServiceName
			FROM otel.otel_traces
			WHERE TraceId = {trace_id:String}`,
			map[string]string{"trace_id": rootTraceID})
		if !assert.NoError(c, err) {
			return
		}

		byService := make(map[string][]span)
		for _, sp := range spans {
			byService[sp.ServiceName] = append(byService[sp.ServiceName], sp)
		}

		for _, name := range services {
			assert.NotEmpty(c, byService[name], "no spans of %s in trace", name)
		}

		for _, sp := range byService["notification"] {
			assert.NotEmpty(c, sp.ParentSpanID, "notification span must not be a root")
		}
	}, traceTimeout, eventuallyTick)

	// Граница не приняла чужой trace-id: ни одного спана с клиентским TraceId.
	foreign, err := querySpans(t.Context(), `
		SELECT TraceId, ParentSpanId, ServiceName
		FROM otel.otel_traces
		WHERE TraceId = {trace_id:String}`,
		map[string]string{"trace_id": clientTraceID})
	require.NoError(t, err)
	assert.Empty(t, foreign, "spans with client trace id")
}

// randomHex возвращает n случайных байт в виде hex-строки (для trace_id n = 16, для span_id n = 8).
func randomHex(t *testing.T, n int) string {
	t.Helper()

	b := make([]byte, n)
	_, err := rand.Read(b)
	require.NoError(t, err)

	return hex.EncodeToString(b)
}
