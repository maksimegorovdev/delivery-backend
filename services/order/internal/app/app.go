package app

import (
	"context"
	"log/slog"

	"buf.build/go/protovalidate"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcclient"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcserver"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/interceptors"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/client"
	repo "github.com/maksimegorovdev/delivery-backend/services/order/internal/repository/pg"
	grpcrouter "github.com/maksimegorovdev/delivery-backend/services/order/internal/transport/grpc"
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/usecase"

	"github.com/maksimegorovdev/delivery-backend/services/order/internal/config"
)

type App struct {
	cfg         *config.Config
	log         *slog.Logger
	pgPool      *pgxpool.Pool
	grpcServer  *grpcserver.Server
	userConn    *grpc.ClientConn
	productConn *grpc.ClientConn
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

	// gRPC Client
	userConn, err := grpcclient.New(cfg.UserService.Addr)
	if err != nil {
		return nil, err
	}

	productConn, err := grpcclient.New(cfg.ProductService.Addr)
	if err != nil {
		return nil, err
	}

	// Repository
	orderRepo := repo.NewOrderRepo(pgPool)

	// Provider
	userClient := client.NewUserClient(userConn)
	productClient := client.NewProductClient(productConn)

	// Usecase
	orderUsecase := usecase.NewOrderUsecase(usecase.OrderUsecaseDeps{
		Orders:   orderRepo,
		Users:    userClient,
		Products: productClient,
	})

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
		Server:       grpcServer.Server(),
		OrderUsecase: orderUsecase,
	})

	app := &App{
		cfg:         cfg,
		log:         log,
		pgPool:      pgPool,
		grpcServer:  grpcServer,
		userConn:    userConn,
		productConn: productConn,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.Info(
			"grpc server started",
			slog.String("addr", a.cfg.GRPCServer.Addr),
		)
		defer a.log.Info(
			"grpc server stopped",
			slog.String("addr", a.cfg.GRPCServer.Addr),
		)
		return a.grpcServer.Run(ctx)
	})

	return g.Wait()
}

func (a *App) Close() error {
	if err := a.productConn.Close(); err != nil {
		return err
	}

	if err := a.userConn.Close(); err != nil {
		return err
	}

	a.pgPool.Close()

	return nil
}
