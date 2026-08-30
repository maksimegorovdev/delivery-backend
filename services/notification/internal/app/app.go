package app

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/config"
)

type App struct {
	cfg    *config.Config
	log    *slog.Logger
	pgPool *pgxpool.Pool
}

func New(ctx context.Context) (*App, error) {
	// Config
	cfg, err := config.New()
	if err != nil {
		return nil, err
	}

	// Logger
	log := logger.New(
		logger.WithLevel(cfg.Log.Level),
		logger.WithFormat(cfg.Log.Format),
	).With(
		slog.String("app_name", cfg.App.Name),
	)
	slog.SetDefault(log)

	// Postgres
	pgPool, err := postgres.New(
		ctx,
		cfg.PG.DSN,
		postgres.WithMaxConns(cfg.PG.MaxConns),
	)
	if err != nil {
		return nil, err
	}

	app := &App{
		cfg:    cfg,
		log:    log,
		pgPool: pgPool,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	return nil
}

func (a *App) Close() error {
	return nil
}
