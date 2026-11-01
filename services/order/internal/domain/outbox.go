package domain

import "time"

const AggregateTypeOrder = "order"

const EventTypeOrderCreated = "order.created"

type OutboxEvent struct {
	ID            string
	AggregateType string
	AggregateID   string
	Type          string
	Payload       []byte
	CreatedAt     time.Time
}
