package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/app"
)

// shutdownTimeout ограничивает время graceful shutdown, включая сброс телеметрии.
const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error(
			"service stopped with error",
			logger.Err(err),
		)
		os.Exit(1)
	}
}

func run() (err error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		return fmt.Errorf("app init: %w", err)
	}
	defer func() {
		// ctx к этому моменту уже отменён сигналом, поэтому Close получает его копию без отмены.
		closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()

		err = errors.Join(err, a.Close(closeCtx))
	}()

	if err = a.Run(ctx); err != nil {
		return fmt.Errorf("app run: %w", err)
	}

	return nil
}
