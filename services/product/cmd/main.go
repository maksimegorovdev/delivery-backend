package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/services/product/internal/app"
)

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
		err = errors.Join(err, a.Close())
	}()

	if err = a.Run(ctx); err != nil {
		return fmt.Errorf("app run: %w", err)
	}

	return nil
}
