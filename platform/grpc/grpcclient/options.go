package grpcclient

import "google.golang.org/grpc"

type Option func(*client)

func WithDialOption(opts ...grpc.DialOption) Option {
	return func(c *client) {
		c.dialOpts = append(c.dialOpts, opts...)
	}
}
