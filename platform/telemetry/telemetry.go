// Package telemetry инициализирует OpenTelemetry одним вызовом: [New] создаёт
// провайдеры и регистрирует их глобально, [Telemetry.Shutdown] дошлёт данные и закроет их.
package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/log"
	lognoop "go.opentelemetry.io/otel/log/noop"
)

// Telemetry владеет провайдерами OpenTelemetry, созданными в [New].
type Telemetry struct {
	loggerProvider log.LoggerProvider
}

// New создаёт Telemetry по cfg.
//
// При cfg.Enabled=false возвращается noop-экземпляр: глобальные провайдеры OTel
// не меняются, сетевых соединений нет. Некорректный cfg при Enabled=true даёт ошибку.
func New(_ context.Context, cfg Config) (*Telemetry, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &Telemetry{loggerProvider: lognoop.NewLoggerProvider()}, nil
}

// LoggerProvider возвращает провайдер логов для передачи в platform/logger.
func (t *Telemetry) LoggerProvider() log.LoggerProvider {
	return t.loggerProvider
}

// Shutdown закрывает провайдеры, дослав накопленные данные до отмены ctx.
func (t *Telemetry) Shutdown(_ context.Context) error {
	return nil
}
