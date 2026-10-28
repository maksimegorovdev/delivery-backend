package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maksimegorovdev/delivery-backend/platform/logger"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/app"
)

// shutdownTimeout ограничивает время graceful shutdown, включая сброс телеметрии.
const shutdownTimeout = 10 * time.Second

func main() {
	os.Exit(run())
}

// run запускает сервис и возвращает код выхода. Ошибки логируются здесь, а не в main:
// ошибка Run пишется до Close, пока LoggerProvider открыт, иначе она не дошла бы до OTel.
// Логгер сервиса снаружи App недоступен, поэтому используется slog по умолчанию:
// app.New делает логгер сервиса логгером по умолчанию.
//
//nolint:sloglint // см. комментарий выше
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		// Телеметрия при ошибке New уже откатилась: запись уходит только в stdout.
		slog.ErrorContext(ctx, "service init failed", logger.Err(err))
		return 1
	}

	code := 0
	if err = a.Run(ctx); err != nil {
		slog.ErrorContext(ctx, "service stopped with error", logger.Err(err))
		code = 1
	}

	// ctx к этому моменту уже отменён сигналом, поэтому Close получает его копию без отмены.
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	if err = a.Close(closeCtx); err != nil {
		// LoggerProvider уже закрыт: запись уходит только в stdout.
		slog.ErrorContext(closeCtx, "service close failed", logger.Err(err))
		code = 1
	}

	return code
}
