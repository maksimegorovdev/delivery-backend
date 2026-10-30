//nolint:testpackage // the test uses unexported package symbols
package kafkaconsumer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/twmb/franz-go/pkg/kgo"
)

func Test_toMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		record *kgo.Record
		want   Message
	}{
		{
			name: "copies all fields",
			record: &kgo.Record{
				Topic:     "order.events.v1",
				Partition: 3,
				Offset:    42,
				Key:       []byte("key"),
				Value:     []byte("value"),
				Headers:   []kgo.RecordHeader{{Key: "traceparent", Value: []byte("00-abc")}},
			},
			want: Message{
				Topic:     "order.events.v1",
				Partition: 3,
				Offset:    42,
				Key:       []byte("key"),
				Value:     []byte("value"),
				Headers:   map[string]string{"traceparent": "00-abc"},
			},
		},
		{
			name:   "no headers gives empty non-nil map",
			record: &kgo.Record{Topic: "t"},
			want:   Message{Topic: "t", Headers: map[string]string{}},
		},
		{
			name: "duplicate header key keeps first value",
			record: &kgo.Record{
				Topic: "t",
				Headers: []kgo.RecordHeader{
					{Key: "k", Value: []byte("first")},
					{Key: "k", Value: []byte("second")},
					{Key: "other", Value: []byte("x")},
				},
			},
			want: Message{Topic: "t", Headers: map[string]string{"k": "first", "other": "x"}},
		},
		{
			name: "nil header value becomes empty string",
			record: &kgo.Record{
				Topic:   "t",
				Headers: []kgo.RecordHeader{{Key: "k"}},
			},
			want: Message{Topic: "t", Headers: map[string]string{"k": ""}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, toMessage(tt.record))
		})
	}
}
