package app

import (
	"context"
	"errors"
	"log/slog"

	"buf.build/go/protovalidate"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/closer"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcserver"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/config"
	repo "github.com/maksimegorovdev/delivery-backend/services/user/internal/repository/pg"
	grpcrouter "github.com/maksimegorovdev/delivery-backend/services/user/internal/transport/grpc"
	"github.com/maksimegorovdev/delivery-backend/services/user/internal/usecase"
)

type App struct {
	cfg        *config.Config
	log        *slog.Logger
	grpcServer *grpcserver.Server
	closer     *closer.Closer
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

	// Proto Validator
	validator, err := protovalidate.New()
	if err != nil {
		return nil, err
	}

	// Repository
	userRepo := repo.NewUserRepo(pgPool)
	addressRepo := repo.NewAddressRepo(pgPool)

	// Usecase
	userUsecase := usecase.NewUserUsecase(userRepo)
	addressUsecase := usecase.NewAddressUsecase(addressRepo)

	// gRPC Server
	grpcServer := grpcserver.New(
		cfg.GRPCServer.Addr,
		grpcserver.WithReflection(cfg.GRPCServer.Reflection),
		grpcserver.WithServerOptions(
			grpc.ChainUnaryInterceptor(
				interceptors.Error(),
				interceptors.Logger(log),
				interceptors.Validation(validator),
			),
		),
	)

	// Router
	grpcrouter.NewRouter(grpcrouter.RouterDeps{
		Server:         grpcServer.Server(),
		UserUsecase:    userUsecase,
		AddressUsecase: addressUsecase,
	})

	app := &App{
		cfg:        cfg,
		log:        log,
		grpcServer: grpcServer,
		closer:     cl,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.InfoContext(ctx,
			"grpc server started",
			slog.String("addr", a.cfg.GRPCServer.Addr),
		)
		defer a.log.InfoContext(ctx,
			"grpc server stopped",
			slog.String("addr", a.cfg.GRPCServer.Addr),
		)
		return a.grpcServer.Run(ctx)
	})

	return g.Wait()
}

func (a *App) Close(ctx context.Context) error {
	return a.closer.Close(ctx)
}
