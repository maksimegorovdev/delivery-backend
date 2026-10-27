# Implementation Plan: logs + tracing + metrics

> Спеки: [SPEC-logs.md](../spec/SPEC-logs.md) · [SPEC-tracing.md](../spec/SPEC-tracing.md) · [SPEC-metrics.md](../spec/SPEC-metrics.md)
> Карта: [SPEC-observability-map.md](../spec/SPEC-observability-map.md) · Задачи: [todo.md](todo.md)
> Предыдущий модуль: [archive/telemetry-sdk-plan.md](archive/telemetry-sdk-plan.md)

## Overview

Три модуля карты одним планом. Все сервисы получают логи в `otel.otel_logs` с `TraceId`/`SpanId`,
сквозные трейсы sync-пути `gateway → order → user/product → Postgres` и спаны обработки Kafka
в notification, а также RED- и runtime-метрики в `otel.otel_metrics_*`. Инструментация встраивается
в `platform` по умолчанию и опирается на глобальные провайдеры из `telemetry-sdk`. При
`OTEL_ENABLED=false` они noop, отдельные флаги не нужны. В `services/*` меняется только `app.go`:
одна строка `logger.WithLoggerProvider(...)`.

## Architecture Decisions

- **Срез = точка интеграции, а не модуль карты.** `otelgrpc`, `otelhttp`, `otelpgx`, `kotel` дают
  и спаны, и метрики. Поэтому каждая задача фазы 2 подключает компонент целиком (trace + metrics)
  и один раз меняет файл. Модуль `metrics` в итоге сводится к runtime-метрикам, каталогу и e2e-проверкам.
- **Порядок: logs → gRPC → HTTP → Postgres → Kafka → runtime.** Логи независимы и самые маленькие.
  gRPC идёт перед HTTP: hop order→user/product виден в трейсах без gateway. Kafka и runtime независимы
  от sync-пути и закрывают фазу.
- **Route-middleware регистрирует `httpserver.New`, а не gateway.** Это принцип «platform по умолчанию».
  `http.route` берётся из `chi.RouteContext` после роутинга и одновременно задаёт имя спана
  и атрибут метрик (`otelhttp.Labeler`). Если `otelhttp` v0.72 уже умеет брать маршрут сам,
  middleware сокращается до имени спана (сверяем в Task 0).
- **`/swagger/*` фильтруется в `otelhttp.WithFilter`.** gRPC `reflection`/`health` фильтруются через
  `otelgrpc.WithFilter` на сервере.
- **Обработка Kafka-записи выносится в функцию** (`processRecord(ctx, record, handler) error` со спаном),
  чтобы тестировать без брокера. Поведение commit/rewind не меняется.
- **E2E-проверки — SQL к ClickHouse** через `docker exec clickhouse clickhouse-client` или Grafana
  Explore → SQL Editor. Готовый скрипт запуска всех 5 сервисов в двух режимах уже проверен при закрытии
  telemetry-sdk (сборка бинарников + `.env` + SIGINT).

## Dependency Graph

```
Task 0: spike — совместимость версий contrib с otel v1.47 (без коммита)
    │
    ├── logs ───────────────────────────────────────────────────────┐
    │   Task 1: stdout trace_id/span_id (traceHandler)              │
    │       └── Task 2: OTel fan-out (otelslog) + пилот gateway     │
    │               └── Task 3: остальные 4 сервиса                 │
    │                                                               │
    ├── sync-трейсы (+ их метрики) ─────────────────────────────────┤
    │   Task 4: gRPC server/client (otelgrpc)                       │
    │       └── Task 5: HTTP gateway (otelhttp, route, request.id)  │
    │               └── Task 6: Postgres (otelpgx tracer + stats)   │
    │                                                               ├── Checkpoint B: e2e трейс
    ├── Task 7: Kafka consumer (kotel tracer + meter, process span) │   заказа + логи с TraceId
    ├── Task 8: runtime-метрики в telemetry.New                     │
    │                                                               │
    └── Task 9: каталог метрик + e2e кардинальность/temporality ◄───┘
            └── Task 10: карта, TODO, Success Criteria трёх спеков
```

Параллельно можно вести: logs (1–3) ∥ sync-трейсы (4–6); Task 7 и Task 8 — в любой момент после Task 0.
Последовательно: 4 → 5 → 6 (общий e2e-трейс), 9 после всех, 10 последним.

## Task List

### Phase 0: Риски
- [x] Task 0: Spike — версии contrib-модулей совместимы с otel v1.47 (без коммита)

### Phase 1: Логи
- [x] Task 1: stdout-логи с `trace_id`/`span_id`
- [ ] Task 2: OTel fan-out в `logger` + пилот gateway
- [ ] Task 3: OTel-логи в order, user, product, notification

### Checkpoint A: логи
- [ ] `task -d platform test`/`lint` зелёные, покрытие `platform/logger` ≥ 80%
- [ ] `SELECT DISTINCT ServiceName FROM otel.otel_logs` — все 5 сервисов; с `OTEL_ENABLED=false` только stdout

### Phase 2: Sync-трейсы и их метрики
- [ ] Task 4: gRPC server и client (`otelgrpc`)
- [ ] Task 5: HTTP gateway (`otelhttp`, route-middleware, `request.id`)
- [ ] Task 6: Postgres (`otelpgx` tracer + `RecordStats`)

### Checkpoint B: сквозной трейс
- [ ] Создание заказа — один `TraceId` со спанами gateway/order/user/product и SQL под gRPC; водопад в Grafana
- [ ] Несуществующий user — `StatusCode = Error` в order и gateway; grpcui не создаёт спанов
- [ ] Логи gateway и order по одному запросу имеют одинаковый `TraceId`

### Phase 3: Async-консьюмер и runtime
- [ ] Task 7: Kafka consumer (`kotel` tracer + meter, спан `process` на запись)
- [ ] Task 8: Runtime-метрики Go

### Checkpoint C: инструментация завершена
- [ ] `task lint`, `go build ./...` во всех модулях go.work, `task -d platform test`
- [ ] Все 5 сервисов стартуют и завершаются в обоих режимах; остановка Collector их не роняет

### Phase 4: Каталог и закрытие
- [ ] Task 9: Каталог метрик `docs/observability/metrics.md` + проверка кардинальности и temporality
- [ ] Task 10: Карта, TODO, Success Criteria трёх спеков

### Checkpoint D: модули завершены
- [ ] Все Success Criteria `SPEC-logs.md`, `SPEC-tracing.md`, `SPEC-metrics.md` отмечены; ревью → `async-propagation`

## Risks and Mitigations

| # | Риск | Влияние | Митигация |
|---|---|---|---|
| R1 | ~~`kotel` v1.7.1, `otelpgx` v0.13.0 или `otelslog` v0.21.0 тянут otel core новее v1.47~~ | ~~High~~ | **Закрыт (Task 0, 2026-10-27):** `go get` всех шести модулей не меняет `otel*` (v1.47.0), `grpc` (v1.83.2), `franz-go` (v1.22.1), `pgx` (v5.10.0); `go build ./...` зелёный во всех 7 модулях go.work. См. таблицу ниже |
| R2 | В gateway нет `GET /api/v1/orders/{id}` — Success Criteria tracing/metrics ссылаются на этот маршрут | Med | **Решено:** шаблонность `http.route` проверяется unit-тестом на chi-маршруте с `{id}`; e2e — на `POST /api/v1/orders`. Формулировки спеков правятся в Task 10 |
| R3 | Рекурсия: error handler пишет WARN в `slog.Default()`, а тот после Task 2 уходит и в OTel. При упавшем Collector ошибка экспорта логов порождает новый лог | Med | Троттлинг 30s уже ограничивает поток. В Task 2 — e2e-проверка: остановленный Collector, сервис 2 минуты, число WARN в stdout ≤ 5 |
| R4 | `otelhttp` оборачивает chi целиком, а `RoutePattern` известен только после роутинга — имя спана в момент старта `"HTTP POST"` | Low | **Уточнено (Task 0):** chi v5.3.2 сам ставит `r.Pattern` (`mux.go:481`), а `otelhttp` v0.72 после `next.ServeHTTP` при `Pattern != ""` вызывает `span.SetName(spanNameFormatter(...))` (`handler.go:196`). Собственный `middleware.Route` вероятно не нужен — Task 5 начинает с unit-теста на имя спана и `http.route` в метрике; middleware пишем, только если тест красный по причине `otelhttp` |
| R5 | `kotel` создаёт спан `process` только через `Tracer.WithProcessSpan(record)` (в плане было `WithProcessingSpan` — имя исправлено по Task 0) — легко забыть `End()` на ветке ошибки/rewind | Med | Логика в `processRecord` с `defer span.End()`; unit-тест: ошибка хендлера → один спан со `StatusCode=Error` |
| R6 | Успешное создание заказа в e2e пишет в локальные БД и outbox → Kafka | Low | Локальное окружение; это и нужно для спана `process` в notification. Для ошибочного пути — фиктивный `address_id` без записи |
| R7 | `otelpgx` может писать параметры SQL в спан | Low | **Уточнено (Task 0):** в v0.13.0 параметры включаются только явно через `WithIncludeQueryParameters(...)`; `WithDisableQuerySpanArgs` не существует. Не передаём эту опцию (и не используем `WithDisableSQLStatementInAttributes` — `db.query.text` нужен). Проверка в ClickHouse остаётся: в `SpanAttributes` нет значений параметров |
| R8 | Покрытие `platform/logger` ≥ 80% — сейчас тестов в пакете нет | Low | Task 1–2 пишут тесты вместе с кодом; `go test -cover` в верификации |

## Результат Task 0: совместимость версий (2026-10-27)

`go get` в `platform` на временной ветке (удалена), `godig` не установлен — API сверены `go doc` и чтением исходников в module cache.

| Модуль | Версия | Что поднимает в go.mod |
|---|---|---|
| `contrib/bridges/otelslog` | v0.21.0 | новый indirect |
| `contrib/instrumentation/net/http/otelhttp` | v0.72.0 | v0.70.0 → v0.72.0; новый indirect `httpsnoop` v1.1.0 |
| `contrib/instrumentation/google.golang.org/grpc/otelgrpc` | v0.72.0 | v0.70.0 → v0.72.0 |
| `contrib/instrumentation/runtime` | v0.72.0 | новый indirect |
| `github.com/exaring/otelpgx` | v0.13.0 | новый indirect |
| `github.com/twmb/franz-go/plugin/kotel` | v1.7.1 | новый indirect; `franz-go` остаётся v1.22.1 |

`otel*` v1.47.0, `grpc` v1.83.2, `pgx` v5.10.0 — без изменений.

Сверка API из плана:

| API | Статус |
|---|---|
| `otelhttp.WithFilter`, `LabelerFromContext`, `WithMeterProvider`/`WithTracerProvider`, `WithSpanNameFormatter` | есть |
| `http.route` / имя спана из chi | `otelhttp` сам переименовывает спан по `r.Pattern`, который ставит chi (см. R4) |
| `otelgrpc.WithFilter`, `filters.ServicePrefix`, `filters.HealthCheck` | есть; `NewServerHandler`/`NewClientHandler` — stats handler |
| `otelpgx.NewTracer`, `RecordStats(db PoolStats)` | есть; параметры SQL выключены по умолчанию (см. R7) |
| `kotel.NewKotel(WithTracer, WithMeter)`, `Kotel.Hooks()` | есть; спан обработки — `Tracer.WithProcessSpan` (см. R5) |
| `otelslog.NewHandler(name, opts...)`, `slog.NewMultiHandler` | есть |
| `runtime.Start(opts...)` | есть |

## Open Questions

Нет.

## Принятые решения (2026-10-27)

- **R2:** `GET /api/v1/orders/{id}` в gateway не добавляем. Шаблонность `http.route` проверяется unit-тестом
  на chi-маршруте с `{id}`, e2e — на `POST /api/v1/orders`. Success Criteria спеков правятся в Task 10.
- **Task 9:** трафик для метрик генерирует системный `ab` (`ab -n 500 -c 10 -p body.json`), новых инструментов
  не ставим. Это генерация данных для проверки каталога, кардинальности и temporality, а не нагрузочное
  тестирование: RPS и задержки не анализируются.
