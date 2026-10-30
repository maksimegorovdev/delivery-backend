package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/maksimegorovdev/delivery-backend/platform/closer"
	"github.com/maksimegorovdev/delivery-backend/platform/grpc/grpcclient"
	"github.com/maksimegorovdev/delivery-backend/platform/http/httpserver"
	"github.com/maksimegorovdev/delivery-backend/platform/http/middleware"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/platform/telemetry"

	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/client"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/config"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/service"
	router "github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/transport/http/v1/order"
)

// initCloseTimeout ограничивает освобождение ресурсов, если New упал на середине:
// иначе сброс телеметрии в недоступный Collector задержал бы выход из сервиса.
const initCloseTimeout = 5 * time.Second

type App struct {
	log        *slog.Logger
	httpServer *httpserver.Server
	lis        net.Listener
	closer     *closer.Closer
}

func New(ctx context.Context) (_ *App, err error) {
	cl := closer.New()
	defer func() {
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), initCloseTimeout)
			defer cancel()

			err = errors.Join(err, cl.Close(closeCtx))
		}
	}()

	// Config
	cfg, err := config.New()
	if err != nil {
		return nil, err
	}

	// Telemetry
	tel, err := newTelemetry(ctx, cfg)
	if err != nil {
		return nil, err
	}
	cl.Add(tel.Shutdown)

	// Logger
	log := newLogger(cfg, tel)
	slog.SetDefault(log)

	// gRPC Client
	orderConn, err := grpcclient.New(cfg.OrderService.Addr)
	if err != nil {
		return nil, err
	}
	cl.Add(closer.WrapErr(orderConn.Close))

	// Provider
	orderClient := client.NewOrderClient(orderConn)

	// Service
	orderService := service.NewOrderService(service.OrderServiceDeps{
		Orders: orderClient,
	})

	// Handler
	orderHandler := order.NewOrderHandler(orderService)

	// HTTP Server
	httpServer := httpserver.New(
		cfg.HTTP.Addr,
		httpserver.WithPublicEndpoint(),
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

	lis, err := listen(ctx, cl, cfg.HTTP.Addr)
	if err != nil {
		return nil, err
	}

	app := &App{
		log:        log,
		httpServer: httpServer,
		lis:        lis,
		closer:     cl,
	}

	return app, nil
}

// listen открывает листенер в New, чтобы занятый порт был ошибкой старта, а Addr() знал фактический адрес.
func listen(ctx context.Context, cl *closer.Closer, addr string) (net.Listener, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("app: listen %s: %w", addr, err)
	}

	// Serve уже закрывает листенер при остановке; повторное закрытие — не ошибка.
	cl.Add(closer.WrapErr(func() error {
		if cerr := lis.Close(); cerr != nil && !errors.Is(cerr, net.ErrClosed) {
			return cerr
		}

		return nil
	}))

	return lis, nil
}

func newTelemetry(ctx context.Context, cfg *config.Config) (*telemetry.Telemetry, error) {
	return telemetry.New(ctx, telemetry.Config{
		Enabled:     cfg.Telemetry.Enabled,
		ServiceName: cfg.App.Name,
		Endpoint:    cfg.Telemetry.Endpoint,
	})
}

func newLogger(cfg *config.Config, tel *telemetry.Telemetry) *slog.Logger {
	return logger.New(
		logger.WithLevel(cfg.Log.Level),
		logger.WithFormat(cfg.Log.Format),
		logger.WithLoggerProvider(tel.LoggerProvider()),
	).With(
		slog.String("app_name", cfg.App.Name),
	)
}

// Addr возвращает фактический адрес HTTP-сервера; при порте 0 в конфиге — порт, выданный ОС.
func (a *App) Addr() net.Addr {
	return a.lis.Addr()
}

func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.InfoContext(ctx,
			"http server started",
			slog.String("addr", a.lis.Addr().String()),
		)
		defer a.log.InfoContext(ctx,
			"http server stopped",
			slog.String("addr", a.lis.Addr().String()),
		)
		return a.httpServer.Serve(ctx, a.lis)
	})

	return g.Wait()
}

func (a *App) Close(ctx context.Context) error {
	return a.closer.Close(ctx)
}
