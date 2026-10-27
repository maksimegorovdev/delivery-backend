package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/maksimegorovdev/delivery-backend/platform/closer"
	"github.com/maksimegorovdev/delivery-backend/platform/kafka/kafkaconsumer"
	"github.com/maksimegorovdev/delivery-backend/platform/logger"
	"github.com/maksimegorovdev/delivery-backend/platform/postgres"
	"github.com/maksimegorovdev/delivery-backend/platform/telemetry"

	repo "github.com/maksimegorovdev/delivery-backend/services/notification/internal/repository/pg"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/service"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka"
	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/transport/kafka/order"

	"github.com/maksimegorovdev/delivery-backend/services/notification/internal/config"
)

// initCloseTimeout ограничивает освобождение ресурсов, если New упал на середине:
// иначе сброс телеметрии в недоступный Collector задержал бы выход из сервиса.
const initCloseTimeout = 5 * time.Second

type App struct {
	cfg      *config.Config
	log      *slog.Logger
	consumer *kafkaconsumer.Consumer
	router   *kafka.Router
	closer   *closer.Closer
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
	log := logger.New(
		logger.WithLevel(cfg.Log.Level),
		logger.WithFormat(cfg.Log.Format),
		logger.WithLoggerProvider(tel.LoggerProvider()),
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

	// TxManager
	txManager := postgres.NewTxManager(pgPool)

	// Kafka consumer
	consumer, err := kafkaconsumer.New(
		cfg.Kafka.Brokers,
		cfg.Kafka.ConsumerGroup,
		cfg.Kafka.Topics,
		kafkaconsumer.WithErrorHandler(func(ctx context.Context, err error) {
			log.ErrorContext(
				ctx,
				"kafka consumer",
				logger.Err(err),
			)
		}),
	)
	if err != nil {
		return nil, err
	}
	cl.Add(closer.Wrap(consumer.Close))

	// Repository
	inboxRepo := repo.NewInboxRepo(pgPool)
	notificationRepo := repo.NewNotificationRepo(pgPool)

	// Service
	notificationService := service.NewNotificationService(service.NotificationServiceDeps{
		Tx:            txManager,
		Inbox:         inboxRepo,
		Notifications: notificationRepo,
	})

	// Router
	router := kafka.NewRouter(log)
	router.Register(order.Topic, order.NewHandler(order.HandlerDeps{
		NotificationService: notificationService,
		Log:                 log,
	}))

	app := &App{
		cfg:      cfg,
		log:      log,
		consumer: consumer,
		router:   router,
		closer:   cl,
	}

	return app, nil
}

func newTelemetry(ctx context.Context, cfg *config.Config) (*telemetry.Telemetry, error) {
	return telemetry.New(ctx, telemetry.Config{
		Enabled:     cfg.Telemetry.Enabled,
		ServiceName: cfg.App.Name,
		Endpoint:    cfg.Telemetry.Endpoint,
	})
}

func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.InfoContext(ctx,
			"kafka consumer started",
			slog.Any("brokers", a.cfg.Kafka.Brokers),
			slog.String("group", a.cfg.Kafka.ConsumerGroup),
			slog.Any("topic", a.cfg.Kafka.Topics),
		)
		defer a.log.InfoContext(ctx,
			"kafka consumer stopped",
			slog.Any("brokers", a.cfg.Kafka.Brokers),
			slog.String("group", a.cfg.Kafka.ConsumerGroup),
			slog.Any("topic", a.cfg.Kafka.Topics),
		)
		a.consumer.Run(ctx, a.router)
		return nil
	})

	return g.Wait()
}

func (a *App) Close(ctx context.Context) error {
	return a.closer.Close(ctx)
}
