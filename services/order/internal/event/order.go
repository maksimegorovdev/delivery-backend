package event

import (
	"uuid"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
	orderv1 "github.com/maksimegorovdev/delivery-backend/proto/gen/go/order/v1"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/domain"
)

type OrderEventBuilder struct{}

func NewOrderEventBuilder() *OrderEventBuilder {
	return &OrderEventBuilder{}
}

func (b *OrderEventBuilder) OrderCreated(order domain.Order) (domain.OutboxEvent, error) {
	id := uuid.NewV7().String()

	items := make([]*orderv1.OrderCreatedItem, 0, len(order.Items))
	for _, item := range order.Items {
		items = append(items, &orderv1.OrderCreatedItem{
			Id:          item.ID,
			ProductId:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice:   item.UnitPrice,
		})
	}

	payload, err := protojson.MarshalOptions{
		UseProtoNames: true,
	}.Marshal(&orderv1.OrderCreated{
		EventId:         id,
		OrderId:         order.ID,
		UserId:          order.UserID,
		Items:           items,
		TotalAmount:     order.TotalAmount,
		DeliveryAddress: order.DeliveryAddress,
		CreatedAt:       timestamppb.New(order.CreatedAt),
	})
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
