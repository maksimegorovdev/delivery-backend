package app

import (
	"context"
	"errors"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/maksimegorovdev/delivery-backend/platform/closer"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcclient"
	"github.com/maksimegorovdev/delivery-backend/platform/http/httpserver"
	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/client"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/config"
	router "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http/v1/order"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/usecase"
)

type App struct {
	cfg        *config.Config
	log        *slog.Logger
	httpServer *httpserver.Server
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

	// gRPC Client
	orderConn, err := grpcclient.New(cfg.OrderService.Addr)
	if err != nil {
		return nil, err
	}
	cl.Add(closer.WrapErr(orderConn.Close))

	// Provider
	orderClient := client.NewOrderClient(orderConn)

	// Usecase
	orderUsecase := usecase.NewOrderUsecase(usecase.OrderUsecaseDeps{
		Orders: orderClient,
	})

	// Handler
	orderHandler := order.NewOrderHandler(orderUsecase)

	// HTTP Server
	httpServer := httpserver.New(
		cfg.HTTP.Addr,
	)
	httpServer.Router().Use(
		middleware.RequestID,
		middleware.Logger(log),
		middleware.Recover,
		middleware.FakeAuth(cfg.FakeAuth.UserID),
	)

	// Router
	router.NewRouter(router.RouterDeps{
		Router:       httpServer.Router(),
		OrderHandler: orderHandler,
	})

	app := &App{
		cfg:        cfg,
		log:        log,
		httpServer: httpServer,
		closer:     cl,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.Info(
			"http server started",
			slog.String("addr", a.cfg.HTTP.Addr),
		)
		defer a.log.Info(
			"http server stopped",
			slog.String("addr", a.cfg.HTTP.Addr),
		)
		return a.httpServer.Run(ctx)
	})

	return g.Wait()
}

func (a *App) Close(ctx context.Context) error {
	return a.closer.Close(ctx)
}
