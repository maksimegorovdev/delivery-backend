// Package listener открывает TCP-листенер сервиса и регистрирует его закрытие в [closer.Closer].
package listener

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/maksimegorovdev/delivery-backend/platform/closer"
)

// Listen открывает TCP-листенер по addr, чтобы занятый порт был ошибкой старта, а не запуска,
// и фактический адрес был известен до Serve (при порте 0 его выдаёт ОС).
//
// Закрытие листенера регистрируется в cl. Serve сервера закрывает листенер при остановке сам,
// поэтому повторное закрытие ([net.ErrClosed]) не считается ошибкой; регистрация нужна на случай,
// когда сервер так и не был запущен.
func Listen(ctx context.Context, cl *closer.Closer, addr string) (net.Listener, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listener: listen %s: %w", addr, err)
	}

	cl.Add(closer.WrapErr(func() error {
		if cerr := lis.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			return cerr
		}

		return nil
	}))

	return lis, nil
}
