package app

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcserver"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"

	"github.com/maksimegorovdev/delivery-backend/services/user/internal/config"
)

type App struct {
	cfg        *config.Config
	log        *slog.Logger
	pgPool     *pgxpool.Pool
	grpcServer *grpcserver.Server
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

	// gRPC Server
	grpcServer := grpcserver.New(
		grpcserver.WithPort(cfg.GRPCServer.Port),
	)

	app := &App{
		cfg:        cfg,
		log:        log,
		pgPool:     pgPool,
		grpcServer: grpcServer,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	if err := a.grpcServer.Start(); err != nil {
		return err
	}
	a.log.Info(
		"grpc server started",
		slog.Int("port", a.cfg.GRPCServer.Port),
	)

	select {
	case <-ctx.Done():
		a.log.Info("received signal from OS, starting graceful shutdown")
	case err := <-a.grpcServer.Notify():
		a.log.Error(
			"received error from grpc server",
			slog.Int("port", a.cfg.GRPCServer.Port),
			logger.Err(err),
		)
	}

	if err := a.grpcServer.Shutdown(); err != nil {
		a.log.Warn(
			"grpc graceful shutdown",
			slog.Int("port", a.cfg.GRPCServer.Port),
			logger.Err(err),
		)
	}
	a.log.Info(
		"grpc server stopped",
		slog.Int("port", a.cfg.GRPCServer.Port),
	)

	return nil
}

func (a *App) Close() error {
	a.pgPool.Close()
	return nil
}
