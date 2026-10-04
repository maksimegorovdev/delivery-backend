package domain

type InboxEvent struct {
	EventID     string
	Topic       string
	Partition   int32
	Offset      int64
	EventType   string
	AggregateID string
}
