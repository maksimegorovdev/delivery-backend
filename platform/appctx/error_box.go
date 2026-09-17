package appctx

import "context"

const errBoxKey ctxKey = "err_box"

type ErrBox struct {
	Err error
}

func WithErrBox(ctx context.Context) context.Context {
	return context.WithValue(ctx, errBoxKey, &ErrBox{})
}

func SetErr(ctx context.Context, err error) {
	if box, ok := ctx.Value(errBoxKey).(*ErrBox); ok {
		box.Err = err
	}
}

func GetErr(ctx context.Context) error {
	if box, ok := ctx.Value(errBoxKey).(*ErrBox); ok {
		return box.Err
	}
	return nil
}
