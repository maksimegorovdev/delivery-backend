package grpcclient

import (
	"fmt"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type client struct {
	dialOpts []grpc.DialOption
}

func New(addr string, opts ...Option) (*grpc.ClientConn, error) {
	c := &client{
		dialOpts: []grpc.DialOption{
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		},
	}

	for _, opt := range opts {
		opt(c)
	}

	conn, err := grpc.NewClient(addr, c.dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("grpcclient: dial %s: %w", addr, err)
	}
	return conn, nil
}
