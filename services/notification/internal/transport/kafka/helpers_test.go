package kafka_test

import "time"

const (
	eventuallyTimeout = 30 * time.Second
	eventuallyTick    = 50 * time.Millisecond
)
