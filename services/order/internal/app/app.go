package app

import (
	"context"
	"log/slog"

	"buf.build/go/protovalidate"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcserver"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/config"
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

	// Proto Validator
	validator, err := protovalidate.New()
	if err != nil {
		return nil, err
	}

	// gRPC Server
	grpcServer := grpcserver.New(
		grpcserver.WithPort(cfg.GRPCServer.Port),
		grpcserver.WithReflection(cfg.GRPCServer.Reflection),
		grpcserver.WithServerOptions(
			grpc.ChainUnaryInterceptor(
				interceptors.Error(),
				interceptors.Logger(log),
				interceptors.Validation(validator),
			),
		),
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
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.Info(
			"grpc server started",
			slog.Int("port", a.cfg.GRPCServer.Port),
		)
		defer a.log.Info(
			"grpc server stopped",
			slog.Int("port", a.cfg.GRPCServer.Port),
		)
		return a.grpcServer.Run(ctx)
	})

	return g.Wait()
}

func (a *App) Close() error {
	a.pgPool.Close()
	return nil
}
