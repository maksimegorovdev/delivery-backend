package closer

import (
	"context"
	"errors"
	"sync"
)

type Func func(ctx context.Context) error

type Closer struct {
	mu    sync.Mutex
	funcs []Func
}

func New() *Closer {
	return &Closer{}
}

func (c *Closer) Add(f Func) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.funcs = append(c.funcs, f)
}

func (c *Closer) Close(ctx context.Context) error {
	c.mu.Lock()
	funcs := c.funcs
	c.funcs = nil
	c.mu.Unlock()

	var errs []error
	for i := len(funcs) - 1; i >= 0; i-- {
		if err := funcs[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func Wrap(f func()) Func {
	return func(ctx context.Context) error {
		f()
		return nil
	}
}

func WrapErr(f func() error) Func {
	return func(ctx context.Context) error {
		return f()
	}
}
