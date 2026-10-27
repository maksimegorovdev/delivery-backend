// Package telemetry инициализирует OpenTelemetry одним вызовом: [New] создаёт
// провайдеры и регистрирует их глобально, [Telemetry.Shutdown] дошлёт данные и закроет их.
package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log"
	lognoop "go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// rollbackTimeout ограничивает закрытие уже созданных провайдеров, если New упал на середине.
const rollbackTimeout = time.Second

// Telemetry владеет провайдерами OpenTelemetry, созданными в [New].
// Все провайдеры nil при Enabled=false.
type Telemetry struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	loggerProvider *sdklog.LoggerProvider
}

// factories — конструкторы провайдеров; тесты подставляют свои, чтобы проверить
// откат при ошибке на середине инициализации.
type factories struct {
	tracer func(context.Context, Config, *resource.Resource) (*sdktrace.TracerProvider, error)
	meter  func(context.Context, Config, *resource.Resource) (*sdkmetric.MeterProvider, error)
	logger func(context.Context, Config, *resource.Resource) (*sdklog.LoggerProvider, error)
}

func defaultFactories() factories {
	return factories{
		tracer: newTracerProvider,
		meter:  newMeterProvider,
		logger: newLoggerProvider,
	}
}

// New создаёт Telemetry по cfg.
//
// При cfg.Enabled=false возвращается noop-экземпляр: глобальные провайдеры OTel
// не меняются, сетевых соединений нет. Некорректный cfg при Enabled=true даёт ошибку.
// Иначе провайдеры, W3C-пропагатор (TraceContext + Baggage) и обработчик ошибок SDK
// регистрируются глобально; экспорт асинхронный, недоступный Collector не мешает запуску.
// Если провайдер не удалось создать, уже созданные закрываются, а глобальное состояние
// остаётся прежним.
func New(ctx context.Context, cfg Config) (*Telemetry, error) {
	return build(ctx, cfg, defaultFactories())
}

func build(ctx context.Context, cfg Config, f factories) (*Telemetry, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	t := &Telemetry{}
	if !cfg.Enabled {
		return t, nil
	}

	res, err := newResource(ctx, cfg.ServiceName)
	if err != nil {
		return nil, err
	}

	// Данных ещё нет, поэтому откат ограничен по времени: иначе финальный flush
	// в недоступный Collector задержал бы старт сервиса до таймаута экспортёра.
	rollback := func(cause error) error {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
		defer cancel()
		return errors.Join(cause, t.Shutdown(rollbackCtx))
	}

	if t.tracerProvider, err = f.tracer(ctx, cfg, res); err != nil {
		return nil, err
	}
	if t.meterProvider, err = f.meter(ctx, cfg, res); err != nil {
		return nil, rollback(err)
	}
	if t.loggerProvider, err = f.logger(ctx, cfg, res); err != nil {
		return nil, rollback(err)
	}

	otel.SetErrorHandler(newErrorHandler(errorLogInterval, time.Now))
	otel.SetTracerProvider(t.tracerProvider)
	otel.SetMeterProvider(t.meterProvider)
	otel.SetLoggerProvider(t.loggerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return t, nil
}

// LoggerProvider возвращает провайдер логов для передачи в platform/logger;
// при Enabled=false — noop.
func (t *Telemetry) LoggerProvider() log.LoggerProvider {
	if t.loggerProvider == nil {
		return lognoop.NewLoggerProvider()
	}
	return t.loggerProvider
}

// Shutdown закрывает провайдеры, дослав накопленные данные до отмены ctx.
// Ошибки закрытия отдельных провайдеров объединяются через [errors.Join].
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	if t.tracerProvider != nil {
		errs = append(errs, t.tracerProvider.Shutdown(ctx))
	}
	if t.meterProvider != nil {
		errs = append(errs, t.meterProvider.Shutdown(ctx))
	}
	if t.loggerProvider != nil {
		errs = append(errs, t.loggerProvider.Shutdown(ctx))
	}
	return errors.Join(errs...)
}
