package appctx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/appctx"
)

type unrelatedKey struct{}

func TestRequestID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  context.Context //nolint:containedctx // таблица тестов хранит готовый контекст
		want string
	}{
		{"value written", appctx.WithRequestID(context.Background(), "req-1"), "req-1"},
		{"empty context", context.Background(), ""},
		{"nil context", nil, ""},
		{"overwritten by inner value", appctx.WithRequestID(appctx.WithRequestID(context.Background(), "a"), "b"), "b"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, appctx.GetRequestID(tc.ctx))
		})
	}
}

func TestUserID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ctx  context.Context //nolint:containedctx // таблица тестов хранит готовый контекст
		want string
	}{
		{"value written", appctx.WithUserID(context.Background(), "user-1"), "user-1"},
		{"empty context", context.Background(), ""},
		{"nil context", nil, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, appctx.GetUserID(tc.ctx))
		})
	}
}

func TestKeys_doNotCollide(t *testing.T) {
	t.Parallel()

	ctx := appctx.WithUserID(appctx.WithRequestID(context.Background(), "req-1"), "user-1")

	assert.Equal(t, "req-1", appctx.GetRequestID(ctx))
	assert.Equal(t, "user-1", appctx.GetUserID(ctx))
}

func TestErrBox(t *testing.T) {
	t.Parallel()

	t.Run("error set is visible through the derived context", func(t *testing.T) {
		t.Parallel()

		boxed := appctx.WithErrBox(context.Background())
		derived := context.WithValue(boxed, unrelatedKey{}, "x")
		want := errors.New("boom")

		appctx.SetErr(derived, want)

		require.ErrorIs(t, appctx.GetErr(boxed), want)
	})

	t.Run("box is empty until error set", func(t *testing.T) {
		t.Parallel()

		require.NoError(t, appctx.GetErr(appctx.WithErrBox(context.Background())))
	})

	t.Run("set without box is a no-op", func(t *testing.T) {
		t.Parallel()

		ctx := context.Background()

		require.NotPanics(t, func() { appctx.SetErr(ctx, errors.New("lost")) })
		require.NoError(t, appctx.GetErr(ctx))
	})
}
