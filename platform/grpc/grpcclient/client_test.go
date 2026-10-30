package grpcclient_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcclient"
)

func TestNew_ReturnsConn(t *testing.T) {
	t.Parallel()

	conn, err := grpcclient.New("127.0.0.1:0")

	require.NoError(t, err)
	require.NotNil(t, conn)
	require.NoError(t, conn.Close())
}

func TestNew_InvalidAddress(t *testing.T) {
	t.Parallel()

	conn, err := grpcclient.New("%zz")

	require.Error(t, err)
	assert.Nil(t, conn)
	assert.ErrorContains(t, err, "grpcclient: dial %zz")
}

func TestNew_AppliesDialOptions(t *testing.T) {
	t.Parallel()

	errIntercepted := errors.New("intercepted")
	interceptor := func(
		context.Context,
		string,
		any,
		any,
		*grpc.ClientConn,
		grpc.UnaryInvoker,
		...grpc.CallOption,
	) error {
		return errIntercepted
	}

	conn, err := grpcclient.New("127.0.0.1:0", grpcclient.WithDialOption(grpc.WithUnaryInterceptor(interceptor)))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	err = conn.Invoke(t.Context(), "/svc/Method", nil, nil)

	require.ErrorIs(t, err, errIntercepted)
}
