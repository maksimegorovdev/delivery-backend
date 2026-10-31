//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Таймауты ожидания. Событие идёт через Debezium (опрос WAL) и Redpanda, поэтому запас большой.
const (
	// eventuallyTimeout — ожидание результата в БД notification.
	eventuallyTimeout = 30 * time.Second
	// eventuallyTick — период проверки в [require.EventuallyWithT].
	eventuallyTick = 500 * time.Millisecond

	// requestTimeout — предел одного HTTP-запроса или запроса к БД.
	requestTimeout = 10 * time.Second

	// eventTypeOrderCreated — тип события в inbox_events и notifications; значение из заголовка
	// event-type в contracts/events/order.created.json.
	eventTypeOrderCreated = "order.created"
	// channelLog — единственный канал уведомлений.
	channelLog = "log"
)

// createOrderRequest — тело POST /api/v1/orders.
type createOrderRequest struct {
	AddressID string      `json:"address_id"`
	Items     []orderItem `json:"items"`
}

// orderItem — позиция в запросе на создание заказа.
type orderItem struct {
	ProductID string `json:"product_id"`
	Quantity  int32  `json:"quantity"`
}

// envelope — конверт ответа gateway; data разбирается отдельно в зависимости от статуса.
type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *errorBody      `json:"error"`
}

// errorBody — тело ошибки в конверте.
type errorBody struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// orderResponse — заказ в ответе API.
type orderResponse struct {
	ID              string              `json:"id"`
	UserID          string              `json:"user_id"`
	Status          string              `json:"status"`
	Items           []orderItemResponse `json:"items"`
	TotalAmount     int64               `json:"total_amount"`
	DeliveryAddress string              `json:"delivery_address"`
}

// orderItemResponse — позиция заказа в ответе API.
type orderItemResponse struct {
	ID          string `json:"id"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	Quantity    int32  `json:"quantity"`
	UnitPrice   int64  `json:"unit_price"`
}

// postOrder отправляет сырое тело в POST /api/v1/orders и возвращает статус и разобранный конверт.
func postOrder(ctx context.Context, body []byte) (int, envelope, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		stack.GatewayURL+"/api/v1/orders",
		bytes.NewReader(body),
	)
	if err != nil {
		return 0, envelope{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, envelope{}, fmt.Errorf("post order: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, envelope{}, fmt.Errorf("read response: %w", err)
	}

	var env envelope
	if len(raw) > 0 {
		if decodeErr := json.Unmarshal(raw, &env); decodeErr != nil {
			return resp.StatusCode, envelope{}, fmt.Errorf("decode envelope %q: %w", raw, decodeErr)
		}
	}

	return resp.StatusCode, env, nil
}

// createOrder создаёт заказ через gateway и требует ответ 201.
func createOrder(t *testing.T, req createOrderRequest) orderResponse {
	t.Helper()

	body, err := json.Marshal(req)
	require.NoError(t, err)

	status, env, err := postOrder(t.Context(), body)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status, "error: %+v", env.Error)
	require.Nil(t, env.Error)

	var order orderResponse
	require.NoError(t, json.Unmarshal(env.Data, &order))
	require.NotEmpty(t, order.ID)

	return order
}

// inboxRow — строка inbox_events.
type inboxRow struct {
	EventID     string
	EventType   string
	AggregateID string
}

// notificationRow — строка notifications.
type notificationRow struct {
	EventID   string
	EventType string
	Channel   string
	Payload   []byte
}

// inboxByOrder возвращает записи inbox_events заказа.
func inboxByOrder(ctx context.Context, orderID string) ([]inboxRow, error) {
	return queryRows[inboxRow](ctx, notificationDB, `
		SELECT event_id::text, event_type, aggregate_id::text
		FROM inbox_events
		WHERE aggregate_id = $1`, orderID)
}

// notificationsByOrder возвращает уведомления заказа. В notifications нет id заказа, поэтому
// связь идёт через inbox_events по event_id.
func notificationsByOrder(ctx context.Context, orderID string) ([]notificationRow, error) {
	return queryRows[notificationRow](ctx, notificationDB, `
		SELECT n.event_id::text, n.event_type, n.channel, n.payload
		FROM notifications n
		JOIN inbox_events i ON i.event_id = n.event_id
		WHERE i.aggregate_id = $1`, orderID)
}

// queryRows выполняет SELECT и собирает строки в структуры по позиции колонок.
func queryRows[T any](ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) ([]T, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}

	result, err := pgx.CollectRows(rows, pgx.RowToStructByPos[T])
	if err != nil {
		return nil, fmt.Errorf("collect rows: %w", err)
	}

	return result, nil
}

// awaitOrderEvent ждёт, пока событие заказа дойдёт до БД notification, и возвращает его строки.
func awaitOrderEvent(t *testing.T, orderID string) ([]inboxRow, []notificationRow) {
	t.Helper()

	var (
		inbox         []inboxRow
		notifications []notificationRow
	)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var err error

		inbox, err = inboxByOrder(t.Context(), orderID)
		if !assert.NoError(c, err) {
			return
		}

		notifications, err = notificationsByOrder(t.Context(), orderID)
		if !assert.NoError(c, err) {
			return
		}

		assert.NotEmpty(c, inbox, "inbox_events")
		assert.NotEmpty(c, notifications, "notifications")
	}, eventuallyTimeout, eventuallyTick)

	return inbox, notifications
}
