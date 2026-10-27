# Implementation Plan: telemetry-sdk

> Спек: [SPEC-telemetry-sdk.md](../spec/SPEC-telemetry-sdk.md) · Карта: [SPEC-observability-map.md](../spec/SPEC-observability-map.md)
> Задачи: [todo.md](todo.md) · Предыдущий модуль: [archive/obs-stack-plan.md](archive/obs-stack-plan.md)

## Overview

Пакет `platform/telemetry` инициализирует OpenTelemetry одним вызовом: Resource, TracerProvider,
MeterProvider, LoggerProvider с OTLP gRPC экспортёрами, глобальная регистрация и W3C-пропагатор.
Пять сервисов вызывают `telemetry.New` в `app.New` (после конфига, до логгера) и закрывают его через
`closer` последним. Инструментации (`otelhttp`, `otelgrpc`, `otelslog` и т. д.) в этом модуле нет —
она появится в `logs`/`tracing`/`metrics`.

## Dependency Graph

```
platform/telemetry: Config + noop-путь (Enabled=false) + task test
    │
    ├── Resource (service.*, OTEL_RESOURCE_ATTRIBUTES, build info)
    │       │
    │       └── TracerProvider + пропагатор + error handler (WARN с троттлингом)
    │               │
    │               ├── MeterProvider (delta temporality, интервал)
    │               │
    │               └── LoggerProvider + общий Shutdown + покрытие ≥ 80%
    │                       │
    │   shutdown-контекст   │
    │   с таймаутом в       │
    │   main.go (5 серв.) ──┤
    │                       └── пилот: order (config + app + .env.example)
    │                               │
    │                               ├── user + product
    │                               └── gateway + notification
    │                                       │
    │                                       └── docs: карта, TODO, Success Criteria
```

Задачи пакета последовательны (каждая расширяет `New`). Таймаут shutdown не зависит от пакета, но
нужен до подключения сервисов. После пилота две пары сервисов независимы.

## Architecture Decisions

- **Порядок в `app.New`:** `config.New` → `telemetry.New` → `logger.New`. Спек говорит «до создания
  логгера»; конфиг нужен раньше, потому что из него берутся `Enabled`/`Endpoint`/`APP_NAME`.
- **`Enabled=false`:** `New` не вызывает `otel.Set*` и возвращает `Telemetry` с noop `LoggerProvider`
  (`go.opentelemetry.io/otel/log/noop`); `Shutdown` → `nil`. Так выполняется AC7 без ветвлений в сервисах.
- **Сэмплер и интервал метрик — из SDK:** `sdktrace` без явного `WithSampler` читает
  `OTEL_TRACES_SAMPLER`/`_ARG`, `PeriodicReader` без `WithInterval` читает `OTEL_METRIC_EXPORT_INTERVAL`.
  Свой код задаёт только дефолты спека (`ParentBased(AlwaysSample)`, 10s), если env не задан. Поведение
  сверить с документацией SDK v1.47.0 при реализации (context7 / godig).
- **Error handler:** `otel.SetErrorHandler` с функцией, которая пишет в `slog.Default()` в момент вызова
  (логгер создаётся после `telemetry.New`) и не чаще раза в 30s; пропущенные ошибки считаются и
  выводятся в следующем `WARN` (`suppressed=N`). Интервал — константа пакета.
- **Shutdown-таймаут (дополнение к спеку):** сейчас все `main.go` передают в `a.Close` контекст
  `signal.NotifyContext`, который к этому моменту уже отменён — `Shutdown` провайдеров сразу вернёт
  `context.Canceled` и ничего не дошлёт. Исправление: `context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)`
  в каждом `main.go`. Это делает выполнимыми AC6 и Success Criteria «Ctrl+C ≤ 10s».
- **Тестовые зависимости — только stdlib** (`testing`, `net` для закрытого порта). Для проверки
  temporality вызывается сам selector; для noop — проверка типов провайдеров и что `otel.GetTracerProvider()`
  не изменился.

## Task List

### Phase 1: Пакет `platform/telemetry`
- [x] Task 1: Каркас пакета, noop-путь и `task -d platform test`
- [x] Task 2: Resource
- [x] Task 3: TracerProvider, пропагатор и error handler
- [x] Task 4: MeterProvider с delta temporality
- [x] Task 5: LoggerProvider, общий Shutdown и покрытие

### Checkpoint A: пакет готов
- [ ] `task -d platform test` и `task -d platform lint` зелёные, покрытие `platform/telemetry` ≥ 80%
- [ ] Ревью API пакета с пользователем (на нём строятся `logs`/`tracing`/`metrics`)

### Phase 2: Подключение сервисов
- [ ] Task 6: Shutdown-контекст с таймаутом в `main.go` всех сервисов
- [ ] Task 7: Пилот — order-service
- [ ] Task 8: user-service и product-service
- [ ] Task 9: gateway-service и notification-service

### Checkpoint B: сервисы
- [ ] `task lint` по всем модулям, `go build ./...` в каждом модуле go.work
- [ ] `task watch-all` с `OTEL_ENABLED=true` и `false` — все 5 сервисов стартуют и завершаются ≤ 10s

### Phase 3: Документация
- [ ] Task 10: Карта, TODO, Success Criteria спека

### Checkpoint C: модуль завершён
- [ ] Все Success Criteria `SPEC-telemetry-sdk.md` отмечены, ревью с пользователем → переход к `logs`/`tracing`/`metrics`

## Risks and Mitigations

| # | Риск | Влияние | Митигация |
|---|---|---|---|
| R1 | Без инструментации сервисы ничего не экспортируют, поэтому Success Criteria «WARN при остановленном Collector» нельзя увидеть e2e в этом модуле | Med | **Решено:** критерий закрывается unit-тестом error handler'а (Task 3) и тестом экспорта на закрытый порт (Task 5); e2e-проверка WARN — в первом из модулей `logs`/`tracing`/`metrics` |
| R2 | `otlploggrpc` v0.23.0 / `sdk/log` — нестабильный API, может отличаться от ожиданий | Low | Сверить API по godig для точной версии в Task 5 до написания кода |
| R3 | SDK читает `OTEL_TRACES_SAMPLER`/`OTEL_METRIC_EXPORT_INTERVAL` не так, как ожидается, при явных опциях | Med | Не передавать явные опции, если env задан; тест с `t.Setenv` на оба случая |
| R4 | gRPC-экспортёр при недоступном Collector блокирует `Shutdown` дольше deadline | Med | Тест Task 5 на закрытый порт с `context.WithTimeout`; при необходимости — `WithTimeout` у экспортёров |
| R5 | Локальные `.env` в gitignore — после изменения `config` сервисы не стартуют без `OTEL_ENABLED` | Low | **Решено:** `OTEL_ENABLED` — `required`, как остальные переменные; в Task 7–9 обновить `.env.example` и локальный `.env` |
| R7 | ozzo `validation.Required` на `bool` отклоняет `false` (zero value) — так уже работает `GRPCServer.Reflection` | Med | На `Enabled` не ставить `validation.Required` (присутствие проверяет тег `env:",required"`); `Endpoint` — `validation.When(Enabled, validation.Required)` |
| R6 | Конфликт версий транзитивных зависимостей (`grpc`, `protobuf`, `genproto`) после добавления OTel в `platform` | Med | Task 1 добавляет зависимости первым шагом; `task tidy` и `go build ./...` по всем модулям go.work |

## Open Questions

Нет. Решения: R1 — unit-тесты, e2e WARN в следующем модуле; `OTEL_ENABLED` — `required` (R5).
