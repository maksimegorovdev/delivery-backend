package event

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
	"uuid"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

// errInvalidUTF8 — строка события не является валидным UTF-8. [json.Marshal] молча подменил бы
// такие байты на U+FFFD, а потребитель получил бы другое значение, чем записал заказ.
var errInvalidUTF8 = errors.New("event: invalid utf-8")

// orderCreatedPayload — JSON-представление события order.created, схема которого описана в
// proto/proto/order/v1/events.proto (OrderCreated). Имена полей — snake_case, как в proto.
// Суммы и количества — JSON-числа: так их пишет [encoding/json] и так они выглядят в ответе API.
// Потребитель разбирает payload в сгенерированный orderv1.OrderCreated через protojson,
// который принимает int64 и числом, и строкой.
type orderCreatedPayload struct {
	EventID         string             `json:"event_id"`
	OrderID         string             `json:"order_id"`
	UserID          string             `json:"user_id"`
	Items           []orderCreatedItem `json:"items"`
	TotalAmount     int64              `json:"total_amount"`
	DeliveryAddress string             `json:"delivery_address"`
	CreatedAt       time.Time          `json:"created_at"`
}

// orderCreatedItem — позиция заказа в событии.
type orderCreatedItem struct {
	ID          string `json:"id"`
	ProductID   string `json:"product_id"`
	ProductName string `json:"product_name"`
	Quantity    int32  `json:"quantity"`
	UnitPrice   int64  `json:"unit_price"`
}

// stringField — строковое поле события с именем для сообщения об ошибке.
type stringField struct {
	name  string
	value string
}

// validate проверяет, что все строки события — валидный UTF-8. Имя позиции с индексом
// форматируется только при ошибке: на успешном пути проверка не выделяет память под строки.
func (p orderCreatedPayload) validate() error {
	for _, f := range [...]stringField{
		{"event_id", p.EventID},
		{"order_id", p.OrderID},
		{"user_id", p.UserID},
		{"delivery_address", p.DeliveryAddress},
	} {
		if !utf8.ValidString(f.value) {
			return fmt.Errorf("%w: %s", errInvalidUTF8, f.name)
		}
	}

	for i, item := range p.Items {
		if name := item.invalidUTF8Field(); name != "" {
			return fmt.Errorf("%w: items[%d].%s", errInvalidUTF8, i, name)
		}
	}

	return nil
}

// invalidUTF8Field возвращает имя первого поля позиции с невалидным UTF-8 или пустую строку.
func (i orderCreatedItem) invalidUTF8Field() string {
	switch {
	case !utf8.ValidString(i.ID):
		return "id"
	case !utf8.ValidString(i.ProductID):
		return "product_id"
	case !utf8.ValidString(i.ProductName):
		return "product_name"
	default:
		return ""
	}
}

type OrderEventBuilder struct{}

func NewOrderEventBuilder() *OrderEventBuilder {
	return &OrderEventBuilder{}
}

func (b *OrderEventBuilder) OrderCreated(order domain.Order) (domain.OutboxEvent, error) {
	id := uuid.NewV7().String()

	items := make([]orderCreatedItem, 0, len(order.Items))
	for _, item := range order.Items {
		items = append(items, orderCreatedItem{
			ID:          item.ID,
			ProductID:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice:   item.UnitPrice,
		})
	}

	body := orderCreatedPayload{
		EventID:         id,
		OrderID:         order.ID,
		UserID:          order.UserID,
		Items:           items,
		TotalAmount:     order.TotalAmount,
		DeliveryAddress: order.DeliveryAddress,
		CreatedAt:       order.CreatedAt.UTC(),
	}
	if err := body.validate(); err != nil {
		return domain.OutboxEvent{}, apperr.Internal().Wrap(err)
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return domain.OutboxEvent{}, apperr.Internal().Wrap(err)
	}

	return domain.OutboxEvent{
		ID:            id,
		AggregateType: domain.AggregateTypeOrder,
		AggregateID:   order.ID,
		Type:          domain.EventTypeOrderCreated,
		Payload:       payload,
		CreatedAt:     order.CreatedAt,
	}, nil
}
