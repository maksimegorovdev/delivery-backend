//go:build integration

package kafkaconsumer_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/maksimegorovdev/delivery-backend/platform/kafka/kafkaconsumer"
	"github.com/maksimegorovdev/delivery-backend/platform/testkit/kafkatest"
)

const (
	eventuallyTimeout = 30 * time.Second
	eventuallyTick    = 50 * time.Millisecond

	// retryBackoff мал, чтобы тесты ретрая шли быстро, но заметно больше тика проверки.
	retryBackoff = 200 * time.Millisecond
)

var errHandlerFailed = errors.New("kafkaconsumer_test: handler failed")

// attempt — одна попытка обработки записи хендлером.
type attempt struct {
	value string
	at    time.Time
}

// probe — фейковый хендлер: запоминает каждую попытку и по failIf решает, упасть ли.
// seen — число предыдущих попыток для того же значения.
type probe struct {
	mu       sync.Mutex
	attempts []attempt
	failIf   func(value string, seen int) bool
}

func (p *probe) Handle(_ context.Context, msg kafkaconsumer.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	value := string(msg.Value)
	seen := 0
	for _, a := range p.attempts {
		if a.value == value {
			seen++
		}
	}
	p.attempts = append(p.attempts, attempt{value: value, at: time.Now()})

	if p.failIf != nil && p.failIf(value, seen) {
		return errHandlerFailed
	}

	return nil
}

func (p *probe) snapshot() []attempt {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.attempts)
}

func (p *probe) values() []string {
	snap := p.snapshot()
	values := make([]string, len(snap))
	for i, a := range snap {
		values[i] = a.value
	}

	return values
}

// running — запущенный Consumer с горутиной Run.
type running struct {
	t        *testing.T
	cancel   context.CancelFunc
	done     chan struct{}
	cons     *kafkaconsumer.Consumer
	stopOnce sync.Once
}

// start запускает Consumer на topic в группе groupOf(topic) и останавливает его в t.Cleanup.
func start(t *testing.T, topic string, handler kafkaconsumer.Handler, opts ...kafkaconsumer.Option) *running {
	t.Helper()

	opts = append([]kafkaconsumer.Option{kafkaconsumer.WithRetryBackoff(retryBackoff)}, opts...)

	cons, err := kafkaconsumer.New(
		kafkatest.Brokers(t),
		groupOf(topic),
		[]string{topic},
		opts...,
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	r := &running{t: t, cancel: cancel, done: make(chan struct{}), cons: cons}

	go func() {
		defer close(r.done)
		cons.Run(ctx, handler)
	}()

	t.Cleanup(r.stop)

	return r
}

// stop отменяет ctx, ждёт выхода Run и закрывает Consumer. Повторный вызов безопасен.
//
// Здесь [sync.Once], а не [sync.OnceFunc]: requireDone падает через [runtime.Goexit], а OnceFunc
// превращает Goexit в panic, который обрушил бы весь тестовый бинарь вместе с параллельными тестами.
func (r *running) stop() {
	r.stopOnce.Do(func() {
		r.cancel()
		r.requireDone()
		r.cons.Close()
	})
}

// requireDone проверяет, что Run завершился.
func (r *running) requireDone() {
	r.t.Helper()

	eventually(r.t, func(c *assert.CollectT) {
		select {
		case <-r.done:
		default:
			assert.Fail(c, "Run is still running")
		}
	})
}

// eventually повторяет check, пока все его проверки не пройдут, но не дольше eventuallyTimeout.
func eventually(t *testing.T, check func(c *assert.CollectT)) {
	t.Helper()

	require.EventuallyWithT(
		t,
		check,
		eventuallyTimeout,
		eventuallyTick,
	)
}

// recs превращает значения в записи без ключа и заголовков.
func recs(values ...string) []*kgo.Record {
	records := make([]*kgo.Record, len(values))
	for i, value := range values {
		records[i] = &kgo.Record{Value: []byte(value)}
	}

	return records
}

// groupOf возвращает группу теста: топик уникален, значит уникальна и группа.
func groupOf(topic string) string {
	return topic + "-group"
}

func TestConsumer_Run_CommitsHandledRecords(t *testing.T) {
	t.Parallel()

	topic := kafkatest.CreateTopic(t, "commit")

	kafkatest.Produce(t, topic, recs("r1", "r2", "r3")...)

	first := &probe{}
	r1 := start(t, topic, first)

	eventually(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"r1", "r2", "r3"}, first.values())
	})

	// Run возвращается только после синхронного коммита текущей пачки.
	r1.stop()

	// Сентинель отличает «ничего не пришло» от «ещё не пришло»: новый consumer группы
	// стартует с закоммиченного оффсета, поэтому его первая запись — сентинель.
	kafkatest.Produce(t, topic, recs("sentinel")...)

	second := &probe{}
	start(t, topic, second)

	eventually(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"sentinel"}, second.values())
	})
}

func TestConsumer_Run_HandlerErrorRewindsAndRetriesBeforeNextRecord(t *testing.T) {
	t.Parallel()

	topic := kafkatest.CreateTopic(t, "retry")

	kafkatest.Produce(t, topic, recs("r1", "r2", "r3")...)

	p := &probe{failIf: func(value string, seen int) bool { return value == "r2" && seen == 0 }}
	start(t, topic, p)

	// r3 не обрабатывается, пока r2 не прошла: партиция откатывается к упавшей записи.
	eventually(t, func(c *assert.CollectT) {
		assert.Equal(c, []string{"r1", "r2", "r2", "r3"}, p.values())
	})

	attempts := p.snapshot()
	assert.GreaterOrEqual(
		t,
		attempts[2].at.Sub(attempts[1].at),
		retryBackoff,
		"повтор должен ждать retryBackoff",
	)
}

func TestConsumer_Run_StopsOnContextCancel(t *testing.T) {
	t.Parallel()

	t.Run("while polling", func(t *testing.T) {
		t.Parallel()

		topic := kafkatest.CreateTopic(t, "cancel-poll")
		kafkatest.Produce(t, topic, recs("r1")...)

		p := &probe{}
		r := start(t, topic, p)

		eventually(t, func(c *assert.CollectT) {
			assert.Equal(c, []string{"r1"}, p.values())
		})

		r.cancel()
		r.requireDone()
	})

	t.Run("during retry backoff", func(t *testing.T) {
		t.Parallel()

		topic := kafkatest.CreateTopic(t, "cancel-backoff")
		kafkatest.Produce(t, topic, recs("r1")...)

		p := &probe{failIf: func(string, int) bool { return true }}
		r := start(
			t,
			topic,
			p,
			kafkaconsumer.WithRetryBackoff(time.Hour),
		)

		eventually(t, func(c *assert.CollectT) {
			assert.Equal(c, []string{"r1"}, p.values())
		})

		r.cancel()
		r.requireDone()
	})
}
