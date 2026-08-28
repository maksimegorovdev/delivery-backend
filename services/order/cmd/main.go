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
		log.Fatalf("Fatal error: %v", err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize app: %w", err)
	}

	if err = a.Run(ctx); err != nil {
		return fmt.Errorf("failed to run app: %w", err)
	}

	return nil
}
