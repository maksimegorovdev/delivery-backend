// Package grpctest поднимает in-memory gRPC-сервер на bufconn для unit-тестов транспорта и gRPC-клиентов.
//
// Пакет не знает о сервисах и интерцепторах: нужные интерцепторы вызывающий передаёт через opts.
// Пакет импортируется только из *_test.go.
package grpctest

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1 << 20

// NewConn поднимает gRPC-сервер на bufconn и возвращает подключённого клиента.
//
// register регистрирует сервисы на сервере. Сервер и соединение закрываются в tb.Cleanup.
func NewConn(tb testing.TB, register func(*grpc.Server), opts ...grpc.ServerOption) *grpc.ClientConn {
	tb.Helper()

	lis := bufconn.Listen(bufSize)

	srv := grpc.NewServer(opts...)
	register(srv)

	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		// Serve возвращает ошибку только при сбое листенера; после Stop это не ошибка теста.
		_ = srv.Serve(lis)
	}()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		srv.Stop()
		<-serveDone
		tb.Fatalf("grpctest: create client: %v", err)
	}

	tb.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
		<-serveDone
		_ = lis.Close()
	})

	return conn
}
