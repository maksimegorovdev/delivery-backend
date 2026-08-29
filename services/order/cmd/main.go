package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/app"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		return fmt.Errorf("app init: %w", err)
	}

	if err = a.Run(ctx); err != nil {
		return fmt.Errorf("app run: %w", err)
	}

	return nil
}
