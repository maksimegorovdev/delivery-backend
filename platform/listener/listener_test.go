package listener_test

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/maksimegorovdev/delivery-backend/platform/closer"
	"github.com/maksimegorovdev/delivery-backend/platform/listener"
)

func TestListen(t *testing.T) {
	t.Parallel()

	t.Run("returns os-assigned address and closes on closer", func(t *testing.T) {
		t.Parallel()

		cl := closer.New()
		lis, err := listener.Listen(t.Context(), cl, "127.0.0.1:0")
		require.NoError(t, err)

		tcpAddr, ok := lis.Addr().(*net.TCPAddr)
		require.True(t, ok)
		assert.NotZero(t, tcpAddr.Port)

		require.NoError(t, cl.Close(context.Background()))

		_, err = lis.Accept()
		require.ErrorIs(t, err, net.ErrClosed)
	})

	t.Run("already closed listener is not an error", func(t *testing.T) {
		t.Parallel()

		cl := closer.New()
		lis, err := listener.Listen(t.Context(), cl, "127.0.0.1:0")
		require.NoError(t, err)

		require.NoError(t, lis.Close())
		require.NoError(t, cl.Close(context.Background()))
	})

	t.Run("busy port fails and registers nothing", func(t *testing.T) {
		t.Parallel()

		var lc net.ListenConfig
		busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { _ = busy.Close() })

		cl := closer.New()
		lis, err := listener.Listen(t.Context(), cl, busy.Addr().String())

		require.Error(t, err)
		assert.Nil(t, lis)
		assert.ErrorContains(t, err, "listener: listen")
		require.NoError(t, cl.Close(context.Background()))
	})
}
