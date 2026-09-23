package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/maksimegorovdev/delivery-backend/platform/closer"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/config"
)

type App struct {
	cfg    *config.Config
	log    *slog.Logger
	closer *closer.Closer
}

func New(ctx context.Context) (_ *App, err error) {
	cl := closer.New()
	defer func() {
		if err != nil {
			err = errors.Join(err, cl.Close(context.Background()))
		}
	}()

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
	cl.Add(closer.Wrap(pgPool.Close))

	app := &App{
		cfg:    cfg,
		log:    log,
		closer: cl,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	return nil
}

func (a *App) Close(ctx context.Context) error {
	return a.closer.Close(ctx)
}
