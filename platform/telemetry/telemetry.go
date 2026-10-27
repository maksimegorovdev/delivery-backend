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
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Telemetry владеет провайдерами OpenTelemetry, созданными в [New].
type Telemetry struct {
	tracerProvider *sdktrace.TracerProvider // nil при Enabled=false
	loggerProvider log.LoggerProvider
}

// New создаёт Telemetry по cfg.
//
// При cfg.Enabled=false возвращается noop-экземпляр: глобальные провайдеры OTel
// не меняются, сетевых соединений нет. Некорректный cfg при Enabled=true даёт ошибку.
// Иначе провайдеры, W3C-пропагатор (TraceContext + Baggage) и обработчик ошибок SDK
// регистрируются глобально; экспорт асинхронный, недоступный Collector не мешает запуску.
func New(ctx context.Context, cfg Config) (*Telemetry, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	t := &Telemetry{loggerProvider: lognoop.NewLoggerProvider()}
	if !cfg.Enabled {
		return t, nil
	}

	res, err := newResource(ctx, cfg.ServiceName)
	if err != nil {
		return nil, err
	}

	t.tracerProvider, err = newTracerProvider(ctx, cfg, res)
	if err != nil {
		return nil, err
	}

	otel.SetErrorHandler(newErrorHandler(errorLogInterval, time.Now))
	otel.SetTracerProvider(t.tracerProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return t, nil
}

// LoggerProvider возвращает провайдер логов для передачи в platform/logger.
func (t *Telemetry) LoggerProvider() log.LoggerProvider {
	return t.loggerProvider
}

// Shutdown закрывает провайдеры, дослав накопленные данные до отмены ctx.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	var errs []error
	if t.tracerProvider != nil {
		errs = append(errs, t.tracerProvider.Shutdown(ctx))
	}
	return errors.Join(errs...)
}
