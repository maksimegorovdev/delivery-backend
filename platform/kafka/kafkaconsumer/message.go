package kafkaconsumer

import "github.com/twmb/franz-go/pkg/kgo"

type Message struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Value     []byte
	Headers   map[string]string
}

func toMessage(rec *kgo.Record) Message {
	headers := make(map[string]string, len(rec.Headers))
	for _, h := range rec.Headers {
		if _, ok := headers[h.Key]; !ok {
			headers[h.Key] = string(h.Value)
		}
	}
	return Message{
		Topic:     rec.Topic,
		Partition: rec.Partition,
		Offset:    rec.Offset,
		Key:       rec.Key,
		Value:     rec.Value,
		Headers:   headers,
	}
}
