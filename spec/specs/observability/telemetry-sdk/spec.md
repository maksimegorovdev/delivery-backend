# Spec: telemetry-sdk

> Status: **done** · Depends on: [obs-stack](../obs-stack/spec.md) (только для e2e-проверки)
> Предоставляет API для модулей `logs`, `tracing`, `metrics`.

## Objective

Дать всем сервисам единый способ инициализировать OpenTelemetry: один вызов в `app.New`, один shutdown
через `closer`. Модули `logs`/`tracing`/`metrics` строятся поверх этого пакета и не создают провайдеры сами.

Пакет `platform/telemetry`:
- создаёт Resource (`service.name`, `service.version`, `service.instance.id`, атрибуты из `OTEL_RESOURCE_ATTRIBUTES`);
- создаёт TracerProvider, MeterProvider, LoggerProvider с OTLP gRPC экспортёрами;
- регистрирует их глобально (`otel.SetTracerProvider`, `otel.SetMeterProvider`, `global.SetLoggerProvider`)
  и W3C-пропагатор (`TraceContext` + `Baggage`);
- отдаёт `LoggerProvider` явно — его потребляет `platform/logger` (модуль `logs`);
- при `Enabled=false` ничего не экспортирует: глобальные провайдеры остаются noop, сервис работает как сейчас.

### Acceptance criteria

1. Все 5 сервисов вызывают `telemetry.New` в `app.New` до создания логгера, `Shutdown` зарегистрирован в `closer`
   первым (закрывается последним — после остановки серверов, чтобы дослать данные).
2. Конфиг в каждом сервисе: `OTEL_ENABLED` (bool) и `OTEL_EXPORTER_OTLP_ENDPOINT` (обязателен при `OTEL_ENABLED=true`),
   валидация через ozzo-validation как у остальных секций.
3. `service.name` = `APP_NAME`; `service.version` — `vcs.revision` из `debug.ReadBuildInfo` (или `unknown`).
4. Метрики экспортируются с **delta temporality** для counter/histogram (упрощает SQL в ClickHouse,
   см. Decisions), интервал экспорта по умолчанию 10s, переопределяется `OTEL_METRIC_EXPORT_INTERVAL`.
5. Семплер: `ParentBased(AlwaysSample)` по умолчанию; стандартные `OTEL_TRACES_SAMPLER`/`OTEL_TRACES_SAMPLER_ARG` работают.
6. При недоступном Collector сервис стартует и работает (экспорт асинхронный, ошибки экспорта идут в `otel.SetErrorHandler` → slog `WARN`), shutdown укладывается в таймаут closer'а.
7. `OTEL_ENABLED=false` → нулевое поведенческое отличие от текущего состояния (stdout-логи, нет сетевых соединений).

## Tech Stack

| Модуль | Версия |
|---|---|
| `go.opentelemetry.io/otel`, `otel/sdk`, `otel/sdk/metric`, `otel/sdk/log`, `otel/log` | `v1.47.0` |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc` | `v1.47.0` |
| `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc` | `v1.47.0` |
| `go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc` | `v0.23.0` |
| `go.opentelemetry.io/otel/semconv` | версия, совпадающая с SDK `v1.47.0` |

Зависимости добавляются только в модуль `platform`; сервисы получают их транзитивно.

## Commands

```bash
task -d platform test          # новая задача: go test -race ./...
task -d platform lint
go build ./...                  # в каждом модуле go.work (или task lint по всем)
task watch-all                  # e2e: сервисы стартуют с OTEL_ENABLED=true
```

## Project Structure

```
platform/telemetry/
  telemetry.go        → New(ctx, Config) (*Telemetry, error); Shutdown(ctx) error; LoggerProvider()
  config.go           → Config{Enabled, ServiceName, Endpoint}; дефолты
  resource.go         → сборка Resource (service.*, FromEnv, build info)
  exporters.go        → OTLP gRPC экспортёры, temporality selector
  telemetry_test.go   → unit-тесты
platform/Taskfile.yml → + test
services/*/internal/config/config.go → + секция Telemetry
services/*/internal/app/app.go       → вызов telemetry.New + closer
services/*/.env.example              → + OTEL_ENABLED, OTEL_EXPORTER_OTLP_ENDPOINT
```

## Code Style

По образцу существующих пакетов `platform` (конструктор + функция `New`, ошибки с префиксом пакета):

```go
// app.New
tel, err := telemetry.New(ctx, telemetry.Config{
	Enabled:     cfg.Telemetry.Enabled,
	ServiceName: cfg.App.Name,
	Endpoint:    cfg.Telemetry.Endpoint,
})
if err != nil {
	return nil, err
}
cl.Add(tel.Shutdown)

// Logger
log := logger.New(
	logger.WithLevel(cfg.Log.Level),
	logger.WithFormat(cfg.Log.Format),
	logger.WithLoggerProvider(tel.LoggerProvider()), // модуль logs
)
```

```go
// platform/telemetry
func (t *Telemetry) Shutdown(ctx context.Context) error {
	return errors.Join(
		t.tracerProvider.Shutdown(ctx),
		t.meterProvider.Shutdown(ctx),
		t.loggerProvider.Shutdown(ctx),
	)
}
```

Соглашения:
- Ошибки оборачиваются с префиксом: `fmt.Errorf("telemetry: trace exporter: %w", err)`.
- Глобальное состояние OTel устанавливается **только** в `telemetry.New`; остальной код берёт провайдеры через `otel.Get*`.
- Doc-комментарии на экспортируемых идентификаторах (`golang-documentation`).

## Testing Strategy

В репозитории пока нет тестов; этот модуль задаёт образец: stdlib `testing`, table-driven, без новых тестовых библиотек.

| Уровень | Что | Как |
|---|---|---|
| Unit | Resource содержит `service.name`, `service.version`, атрибуты из `OTEL_RESOURCE_ATTRIBUTES` | `t.Setenv`, проверка `resource.Attributes()` |
| Unit | `Enabled=false` → noop-провайдеры, без сетевых соединений, `Shutdown` → nil | проверка типов/поведения провайдеров |
| Unit | Temporality selector: counter/histogram → Delta, UpDownCounter → Cumulative | вызов selector'а напрямую |
| Unit | `Shutdown` с недоступным endpoint укладывается в ctx deadline | `context.WithTimeout`, endpoint на закрытый порт |
| E2E | сервис со `OTEL_ENABLED=true` → в ClickHouse появляются строки с `ServiceName = <APP_NAME>` | вручную после модулей `logs`/`tracing`/`metrics`; в этом модуле — только что сервис стартует и корректно завершается |

Покрытие `platform/telemetry` ≥ 80% (`go test -cover`).

## Boundaries

- **Always:** `go test -race ./...` и `golangci-lint` в `platform` перед коммитом; провайдеры закрываются через closer; ошибки экспорта не валят сервис.
- **Ask first:** добавление зависимостей сверх списка в Tech Stack; использование `contrib/otelconf` (декларативный YAML-конфиг) вместо кода; HTTP-экспортёр вместо gRPC.
- **Never:** вызывать `otel.Set*` вне `platform/telemetry`; хранить endpoint/креды в коде; делать старт сервиса зависимым от доступности Collector.

## Success Criteria

- [x] `task -d platform test` зелёный, покрытие `platform/telemetry` ≥ 80% (91.1%).
- [x] Все 5 сервисов собираются и стартуют через `task watch-all` с `OTEL_ENABLED=true` и с `false`.
- [x] Остановка Collector (`docker compose stop otel-collector`) не роняет сервисы; в логах — `WARN` об ошибке экспорта, не чаще раза в N секунд. _Закрыто unit-тестами error handler; e2e подтверждено 2026-10-27: MeterProvider экспортирует и без инструментации, в логах всех 5 сервисов — `WARN telemetry: export error`._
- [x] `Ctrl+C` завершает сервис ≤ 10s даже при недоступном Collector.
- [x] `.env.example` всех сервисов содержат новые переменные.

## Decisions

- **Delta temporality** для counter и histogram: в ClickHouse каждая точка — приращение за интервал, RPS и
  перцентили считаются `sum()` по окну без вычисления разностей между соседними cumulative-точками.
  UpDownCounter/gauge остаются cumulative (так требует спецификация).
- **Свой `Config`, а не только `OTEL_*`**: явный `Enabled`/`Endpoint` в конфиге сервиса — в стиле проекта
  (валидация при старте). Остальные стандартные `OTEL_*` (sampler, resource attrs, интервал) читает SDK.

## Open Questions

Нет.
