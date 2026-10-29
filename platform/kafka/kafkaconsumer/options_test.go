//nolint:testpackage // the test uses unexported Consumer fields
package kafkaconsumer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestOptions(t *testing.T) {
	t.Parallel()

	t.Run("WithClientOptions appends in order", func(t *testing.T) {
		t.Parallel()

		c := &Consumer{}
		WithClientOptions(kgo.ClientID("a"))(c)
		WithClientOptions(kgo.ClientID("b"), kgo.ClientID("c"))(c)

		assert.Len(t, c.clientOpts, 3)
	})

	t.Run("WithErrorHandler sets handler", func(t *testing.T) {
		t.Parallel()

		var got error
		c := &Consumer{}
		WithErrorHandler(func(_ context.Context, err error) { got = err })(c)

		wantErr := errors.New("boom")
		c.onError(context.Background(), wantErr)

		assert.Equal(t, wantErr, got)
	})

	t.Run("WithMaxPollRecords sets limit", func(t *testing.T) {
		t.Parallel()

		c := &Consumer{}
		WithMaxPollRecords(7)(c)

		assert.Equal(t, 7, c.maxPollRecords)
	})

	t.Run("WithRetryBackoff sets backoff", func(t *testing.T) {
		t.Parallel()

		c := &Consumer{}
		WithRetryBackoff(3 * time.Second)(c)

		assert.Equal(t, 3*time.Second, c.retryBackoff)
	})
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()

		c, err := New([]string{"127.0.0.1:1"}, "group", []string{"topic"})
		require.NoError(t, err)
		t.Cleanup(c.Close)

		assert.Equal(t, defaultMaxPollRecords, c.maxPollRecords)
		assert.Equal(t, defaultRetryBackoff, c.retryBackoff)
		assert.NotNil(t, c.onError)
		assert.NotPanics(t, func() { c.onError(context.Background(), errors.New("ignored")) })
	})

	t.Run("applies options", func(t *testing.T) {
		t.Parallel()

		c, err := New([]string{"127.0.0.1:1"}, "group", []string{"topic"},
			WithMaxPollRecords(5), WithRetryBackoff(time.Minute))
		require.NoError(t, err)
		t.Cleanup(c.Close)

		assert.Equal(t, 5, c.maxPollRecords)
		assert.Equal(t, time.Minute, c.retryBackoff)
	})

	t.Run("invalid client option returns wrapped error", func(t *testing.T) {
		t.Parallel()

		// In regex mode the topic "[" is an invalid expression, so kgo.NewClient returns an error.
		c, err := New([]string{"127.0.0.1:1"}, "group", []string{"["},
			WithClientOptions(kgo.ConsumeRegex()))

		require.Error(t, err)
		assert.Nil(t, c)
		assert.ErrorContains(t, err, "kafkaconsumer: client")
	})
}
