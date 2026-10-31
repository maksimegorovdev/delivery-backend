//go:build e2e

package e2e_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// stableWindow — окно стабилизации: единственное место, где ожидание по времени допустимо.
// Им доказывается отсутствие события: за это время счётчик строк по заказу не должен вырасти.
const stableWindow = 3 * time.Second

// requireOrderEventExactlyOnce дожидается события заказа в БД notification и убеждается,
// что за stableWindow оно не продублировалось: по одной строке в inbox_events и notifications.
func requireOrderEventExactlyOnce(t *testing.T, orderID string) {
	t.Helper()

	inbox, notifications := awaitOrderEvent(t, orderID)
	require.Len(t, inbox, 1, "inbox_events после обработки")
	require.Len(t, notifications, 1, "notifications после обработки")

	time.Sleep(stableWindow)

	inbox, err := inboxByOrder(t.Context(), orderID)
	require.NoError(t, err)
	require.Len(t, inbox, 1, "inbox_events после окна стабилизации")

	notifications, err = notificationsByOrder(t.Context(), orderID)
	require.NoError(t, err)
	require.Len(t, notifications, 1, "notifications после окна стабилизации")
}

// newSeedOrder создаёт заказ с одним seed-товаром и требует ответ 201.
func newSeedOrder(t *testing.T) orderResponse {
	t.Helper()

	return createOrder(t, createOrderRequest{
		AddressID: seed.AddressID,
		Items:     []orderItem{{ProductID: seed.Borjomi.ID, Quantity: 1}},
	})
}

func TestResilience_NotificationDown(t *testing.T) { //nolint:paralleltest // останавливает общий контейнер
	stopService(t, serviceNotification)

	order := newSeedOrder(t)

	startService(t, serviceNotification)

	requireOrderEventExactlyOnce(t, order.ID)
}

func TestResilience_DebeziumDown(t *testing.T) { //nolint:paralleltest // останавливает общий контейнер
	stopService(t, serviceDebezium)

	order := newSeedOrder(t)

	startService(t, serviceDebezium)

	requireOrderEventExactlyOnce(t, order.ID)
}
