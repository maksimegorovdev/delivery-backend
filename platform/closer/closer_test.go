package closer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/closer"
)

type ctxKey struct{}

func TestCloser_Close(t *testing.T) {
	t.Parallel()

	errA := errors.New("a failed")
	errB := errors.New("b failed")

	t.Run("closes in reverse order of registration", func(t *testing.T) {
		t.Parallel()

		var order []string
		c := closer.New()
		for _, name := range []string{"first", "second", "third"} {
			c.Add(closer.Wrap(func() { order = append(order, name) }))
		}

		require.NoError(t, c.Close(context.Background()))
		assert.Equal(t, []string{"third", "second", "first"}, order)
	})

	t.Run("empty closer returns nil", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, closer.New().Close(context.Background()))
	})

	t.Run("collects all errors and keeps closing after a failure", func(t *testing.T) {
		t.Parallel()

		var closed []string
		c := closer.New()
		c.Add(func(context.Context) error { closed = append(closed, "a"); return errA })
		c.Add(closer.Wrap(func() { closed = append(closed, "ok") }))
		c.Add(func(context.Context) error { closed = append(closed, "b"); return errB })

		err := c.Close(context.Background())

		require.ErrorIs(t, err, errA)
		require.ErrorIs(t, err, errB)
		assert.Equal(t, []string{"b", "ok", "a"}, closed)
	})

	t.Run("passes context to every func", func(t *testing.T) {
		t.Parallel()

		ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
		var got []any
		c := closer.New()
		for range 2 {
			c.Add(func(ctx context.Context) error {
				got = append(got, ctx.Value(ctxKey{}))
				return nil
			})
		}

		require.NoError(t, c.Close(ctx))
		assert.Equal(t, []any{"marker", "marker"}, got)
	})

	t.Run("second close does not rerun funcs", func(t *testing.T) {
		t.Parallel()

		calls := 0
		c := closer.New()
		c.Add(closer.Wrap(func() { calls++ }))

		require.NoError(t, c.Close(context.Background()))
		require.NoError(t, c.Close(context.Background()))
		assert.Equal(t, 1, calls)
	})
}

func TestWrapErr(t *testing.T) {
	t.Parallel()

	want := errors.New("close failed")
	c := closer.New()
	c.Add(closer.WrapErr(func() error { return want }))

	require.ErrorIs(t, c.Close(context.Background()), want)
}

func TestCloser_Add_concurrent(t *testing.T) {
	t.Parallel()

	const n = 50
	c := closer.New()
	done := make(chan struct{})
	for range n {
		go func() {
			c.Add(closer.Wrap(func() {}))
			done <- struct{}{}
		}()
	}
	for range n {
		<-done
	}

	require.NoError(t, c.Close(context.Background()))
}
