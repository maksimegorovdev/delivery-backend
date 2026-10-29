package apperr_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/apperr"
)

func TestError_Error(t *testing.T) {
	t.Parallel()

	cause := errors.New("db down")

	tests := []struct {
		name string
		err  *apperr.Error
		want string
	}{
		{"public message of code when message empty", apperr.NotFound(), apperr.MessageNotFound},
		{"custom message", apperr.NotFound().WithMessage("order missing"), "order missing"},
		{"cause appended", apperr.Internal().Wrap(cause), apperr.MessageInternal + ": db down"},
		{"message and cause", apperr.Internal().WithMessage("save failed").Wrap(cause), "save failed: db down"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.EqualError(t, tc.err, tc.want)
		})
	}
}

func TestError_Public(t *testing.T) {
	t.Parallel()

	t.Run("custom message wins", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, "order missing", apperr.NotFound().WithMessage("order missing").Public())
	})

	t.Run("falls back to code message", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, apperr.MessageNotFound, apperr.NotFound().Public())
	})

	t.Run("does not leak cause", func(t *testing.T) {
		t.Parallel()

		err := apperr.Internal().Wrap(errors.New("secret dsn"))

		require.Equal(t, apperr.MessageInternal, err.Public())
	})
}

func TestError_Unwrap(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")

	t.Run("returns cause", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, cause, apperr.Internal().Wrap(cause).Unwrap())
	})

	t.Run("nil without cause", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, apperr.Internal().Unwrap())
	})
}

func TestError_Is(t *testing.T) {
	t.Parallel()

	cause := errors.New("boom")

	t.Run("matches sentinel with same code", func(t *testing.T) {
		t.Parallel()

		err := apperr.NotFound().WithReason("ORDER_NOT_FOUND").Wrap(cause)

		require.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("does not match sentinel with other code", func(t *testing.T) {
		t.Parallel()

		require.NotErrorIs(t, apperr.NotFound(), apperr.ErrInternal)
	})

	t.Run("does not match foreign error", func(t *testing.T) {
		t.Parallel()

		require.NotErrorIs(t, apperr.NotFound(), cause)
	})

	t.Run("finds wrapped cause", func(t *testing.T) {
		t.Parallel()

		require.ErrorIs(t, apperr.Internal().Wrap(cause), cause)
	})

	t.Run("finds app error through fmt wrapping", func(t *testing.T) {
		t.Parallel()

		err := fmt.Errorf("service: %w", apperr.NotFound())

		require.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestError_As(t *testing.T) {
	t.Parallel()

	cause := &customError{}

	t.Run("finds app error", func(t *testing.T) {
		t.Parallel()

		err := fmt.Errorf("wrap: %w", apperr.NotFound().WithReason("R"))

		var target *apperr.Error
		require.ErrorAs(t, err, &target)
		require.Equal(t, "R", target.Reason)
	})

	t.Run("finds cause through app error", func(t *testing.T) {
		t.Parallel()

		err := apperr.Internal().Wrap(cause)

		var target *customError
		require.ErrorAs(t, err, &target)
		require.Same(t, cause, target)
	})
}

type customError struct{}

func (*customError) Error() string { return "custom" }

func TestAs(t *testing.T) {
	t.Parallel()

	t.Run("app error", func(t *testing.T) {
		t.Parallel()

		want := apperr.NotFound()

		got, ok := apperr.As(fmt.Errorf("wrap: %w", want))

		require.True(t, ok)
		require.Same(t, want, got)
	})

	t.Run("foreign error", func(t *testing.T) {
		t.Parallel()

		got, ok := apperr.As(errors.New("plain"))

		require.False(t, ok)
		require.Nil(t, got)
	})

	t.Run("nil", func(t *testing.T) {
		t.Parallel()

		got, ok := apperr.As(nil)

		require.False(t, ok)
		require.Nil(t, got)
	})
}

func TestFrom(t *testing.T) {
	t.Parallel()

	t.Run("nil stays nil", func(t *testing.T) {
		t.Parallel()

		require.Nil(t, apperr.From(nil))
	})

	t.Run("app error returned as is", func(t *testing.T) {
		t.Parallel()

		want := apperr.NotFound()

		require.Same(t, want, apperr.From(fmt.Errorf("wrap: %w", want)))
	})

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()

		got := apperr.From(context.Canceled)

		require.Equal(t, apperr.CodeCanceled, got.Code)
		require.ErrorIs(t, got, context.Canceled)
	})

	t.Run("context deadline exceeded", func(t *testing.T) {
		t.Parallel()

		got := apperr.From(fmt.Errorf("call: %w", context.DeadlineExceeded))

		require.Equal(t, apperr.CodeDeadlineExceeded, got.Code)
		require.ErrorIs(t, got, context.DeadlineExceeded)
	})

	t.Run("unknown error becomes internal", func(t *testing.T) {
		t.Parallel()

		cause := errors.New("boom")

		got := apperr.From(cause)

		require.Equal(t, apperr.CodeInternal, got.Code)
		require.ErrorIs(t, got, cause)
	})
}

func TestError_LogValue(t *testing.T) {
	t.Parallel()

	t.Run("minimal", func(t *testing.T) {
		t.Parallel()

		got := apperr.NotFound().LogValue()

		require.Equal(t, slog.KindGroup, got.Kind())
		require.Equal(t, []slog.Attr{
			slog.String("code", "NOT_FOUND"),
			slog.String("public_message", apperr.MessageNotFound),
		}, got.Group())
	})

	t.Run("full", func(t *testing.T) {
		t.Parallel()

		v := apperr.Violation{Field: "email", Message: "bad"}
		err := apperr.InvalidArgument().
			WithReason("R").
			WithMessage("msg").
			Wrap(errors.New("boom")).
			WithViolations(v)

		got := err.LogValue().Group()

		require.Equal(t, []slog.Attr{
			slog.String("code", "INVALID_ARGUMENT"),
			slog.String("public_message", "msg"),
			slog.String("reason", "R"),
			slog.String("cause", "boom"),
			slog.Any("violations", []apperr.Violation{v}),
		}, got)
	})
}
