package kafkaconsumer

import (
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type Option func(*Consumer)

func WithClientOptions(opts ...kgo.Opt) Option {
	return func(c *Consumer) {
		c.clientOpts = append(c.clientOpts, opts...)
	}
}

func WithErrorHandler(handler ErrorHandler) Option {
	return func(c *Consumer) {
		c.onError = handler
	}
}

func WithMaxPollRecords(n int) Option {
	return func(c *Consumer) {
		c.maxPollRecords = n
	}
}

func WithRetryBackoff(retryBackoff time.Duration) Option {
	return func(c *Consumer) {
		c.retryBackoff = retryBackoff
	}
}
