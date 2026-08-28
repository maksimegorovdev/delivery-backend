package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/maksimegorovdev/delivery-backend/platform/logger"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/config"
)

type App struct {
	log *slog.Logger
}

func New(ctx context.Context) (*App, error) {
	cfg, err := config.New()
	if err != nil {
		return nil, fmt.Errorf("configuration initialization: %w", err)
	}

	log := logger.New(
		logger.WithLevel(cfg.Log.Level),
		logger.WithFormat(cfg.Log.Format),
	)
	log = log.With(
		slog.String("app_name", cfg.App.Name),
	)

	app := &App{
		log: log,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	return nil
}
