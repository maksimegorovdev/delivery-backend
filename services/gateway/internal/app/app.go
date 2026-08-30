package app

import (
	"context"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/maksimegorovdev/delivery-backend/platform/http/httpserver"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/services/gateway/internal/config"
)

type App struct {
	cfg        *config.Config
	log        *slog.Logger
	httpServer *httpserver.Server
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

	// HTTP Server
	httpServer := httpserver.New(
		httpserver.WithPort(cfg.HTTP.Port),
	)

	app := &App{
		cfg:        cfg,
		log:        log,
		httpServer: httpServer,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.Info(
			"http server started",
			slog.Int("port", a.cfg.HTTP.Port),
		)
		defer a.log.Info(
			"http server stopped",
			slog.Int("port", a.cfg.HTTP.Port),
		)
		return a.httpServer.Run(ctx)
	})

	return g.Wait()
}

func (a *App) Close() error {
	return nil
}
