package domain

import "time"

const AggregateTypeOrder = "order"

const EventTypeOrderCreated = "OrderCreated"

type OutboxEvent struct {
	ID            string
	AggregateType string
	AggregateID   string
	Type          string
	Payload       []byte
	CreatedAt     time.Time
}
