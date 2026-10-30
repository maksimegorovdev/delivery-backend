//go:build integration

package pg_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/testkit/pgtest"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/event"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/repository/pg"
)

const (
	goldenOrderCreated = "order.created.json"
	uuidVersionV7      = 7
)

// message — сообщение в Kafka после Debezium EventRouter (deploy/debezium/order-outbox.json):
// aggregate_id → key, type → headers.event-type, payload → value.
type message struct {
	Key     string            `json:"key"`
	Headers map[string]string `json:"headers"`
	Value   map[string]any    `json:"value"`
}

// readGolden читает эталон события из contracts/events.
func readGolden(t *testing.T, name string) message {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(contractsDir, name))
	require.NoError(t, err, "эталон отсутствует: создайте его запуском с -update")

	var msg message
	require.NoError(t, json.Unmarshal(raw, &msg))

	return msg
}

func writeGolden(t *testing.T, name string, msg message) {
	t.Helper()

	raw, err := json.MarshalIndent(msg, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(contractsDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(contractsDir, name), append(raw, '\n'), 0o600))
}

func TestOrderCreated_Golden(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	outbox := pg.NewOutboxRepo(pool)

	evt, err := event.NewOrderEventBuilder().OrderCreated(fixtureOrder())
	require.NoError(t, err)
	require.NoError(t, outbox.Save(t.Context(), evt))

	// Сообщение строится по строке outbox, как это делает EventRouter.
	var (
		rowID   string
		msg     = message{Headers: map[string]string{}}
		payload []byte
		evtType string
	)
	require.NoError(t, pool.QueryRow(
		t.Context(),
		`SELECT id::text, aggregate_id::text, type, payload FROM outbox_events WHERE id = $1`,
		evt.ID,
	).Scan(&rowID, &msg.Key, &evtType, &payload))
	msg.Headers["event-type"] = evtType
	require.NoError(t, json.Unmarshal(payload, &msg.Value))

	// event_id генерируется, поэтому формат проверяется отдельно, а в сравнение идёт значение эталона.
	eventID, ok := msg.Value["event_id"].(string)
	require.True(t, ok, "event_id должен быть строкой")
	assert.Equal(t, rowID, eventID, "event_id в payload совпадает с id строки outbox")
	parsed, err := uuid.Parse(eventID)
	require.NoError(t, err)
	assert.EqualValues(t, uuidVersionV7, parsed[6]>>4, "event_id — UUIDv7")

	if *update {
		msg.Value["event_id"] = fixtureEventID
		writeGolden(t, goldenOrderCreated, msg)

		return
	}

	golden := readGolden(t, goldenOrderCreated)
	msg.Value["event_id"] = golden.Value["event_id"]

	want, err := json.Marshal(golden)
	require.NoError(t, err)
	got, err := json.Marshal(msg)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
}
