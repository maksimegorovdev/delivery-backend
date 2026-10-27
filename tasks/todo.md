# Tasks: logs + tracing + metrics

> План: [plan.md](plan.md) · Спеки: [logs](../spec/SPEC-logs.md) · [tracing](../spec/SPEC-tracing.md) · [metrics](../spec/SPEC-metrics.md)

E2E-запросы к ClickHouse: `docker exec clickhouse clickhouse-client -q "<SQL>"` или Grafana Explore → SQL Editor.
Запуск всех сервисов: `task watch-all` (или бинарники + `.env`, как при закрытии telemetry-sdk).

## Task 0: Spike — совместимость версий contrib с otel v1.47

**Description:** В отдельной ветке выполнить `go get` для `otelslog` v0.21.0, `otelhttp`/`otelgrpc`/`runtime`
v0.72.0, `otelpgx` v0.13.0, `kotel` v1.7.1 в `platform`. Посмотреть, поднимают ли они `go.opentelemetry.io/otel*`
выше v1.47.0 и `grpc`/`franz-go`. Через godig сверить API, на которое опирается план: `otelhttp.WithFilter`,
`otelhttp.LabelerFromContext`, умеет ли `otelhttp` сам выставлять `http.route`, `otelgrpc.WithFilter`
и `filters.ServicePrefix`, опцию отключения аргументов SQL в `otelpgx`, `otelpgx.RecordStats`,
`kotel.WithTracer`/`WithMeter`/`WithProcessSpan`. Без коммита кода.

**Acceptance criteria:**
- [x] Таблица «модуль → версия → что поднимает» дописана в Risks плана (R1 закрыт или эскалирован пользователю)
- [x] Подтверждены или скорректированы API из Architecture Decisions (route-middleware, фильтры, аргументы SQL)

**Verification:**
- [x] `go build ./...` во всех модулях go.work на spike-ветке
- [x] Spike-ветка удалена, рабочая ветка без изменений `go.mod`

**Dependencies:** None

**Files likely touched:** `tasks/plan.md` (только Risks)

**Estimated scope:** XS

## Task 1: stdout-логи с `trace_id`/`span_id`

**Description:** `platform/logger/trace.go`: хендлер-обёртка `traceHandler`. Если в `ctx` есть валидный
`trace.SpanContext`, он добавляет `trace_id`/`span_id` в запись. `New` строит `CtxHandler(traceHandler(base))`.
Без span вывод не меняется. Тесты пакета `logger` — с нуля.

**Acceptance criteria:**
- [x] С валидным SpanContext в ctx stdout содержит `trace_id=<32 hex>` и `span_id=<16 hex>`, без него — не содержит
- [x] Без span вывод байт-в-байт совпадает с golden-строкой текущего формата (text и json)
- [x] `request_id` выводится как раньше, `WithAttrs`/`WithGroup` сохраняют обёртку

**Verification:**
- [x] `task -d platform test` (`bytes.Buffer`, `trace.ContextWithSpanContext` с фиксированным SpanContext)
- [x] `task -d platform lint`

**Dependencies:** Task 0

**Files likely touched:**
- `platform/logger/trace.go`, `platform/logger/logger.go`
- `platform/logger/logger_test.go`

**Estimated scope:** S

## Task 2: OTel fan-out в `logger` + пилот gateway

**Description:** Опция `WithLoggerProvider(log.LoggerProvider)`. С ней `New` строит
`CtxHandler(slog.NewMultiHandler(traceHandler(base), otelslog.NewHandler(...)))`. Уровень проверяется
до fan-out через `slog.HandlerOptions.Level` / обёртку `Enabled`. В gateway `app.go` передаётся
`logger.WithLoggerProvider(tel.LoggerProvider())`. При `Enabled=false` это noop, ветвление не нужно.

**Acceptance criteria:**
- [x] OTel-запись (SimpleProcessor + fake exporter вместо logtest — отдельный модуль) — TraceID/SpanID из ctx, `request_id` в атрибутах, нет дублей `trace_id`/`span_id`; группы `http.*` развёрнуты; Severity соответствует уровню slog
- [x] Записи ниже `LOG_LEVEL` не попадают ни в один хендлер (table-driven); без опции поведение как после Task 1
- [ ] Gateway с `OTEL_ENABLED=true` → строки `ServiceName='gateway'` в `otel.otel_logs`, включая логи без ctx (`http server started`, пустой `TraceId`)

**Verification:**
- [x] `task -d platform test`; `cd platform && go test -cover ./logger/...` ≥ 80% (95.4%)
- [x] `task -d platform lint`, `task -d services/gateway lint`
- [ ] R3: Collector остановлен, gateway 2 минуты → в stdout ≤ 5 `WARN telemetry: export error`, сервис жив

**Dependencies:** Task 1

**Files likely touched:**
- `platform/logger/options.go`, `platform/logger/logger.go`, `platform/logger/logger_test.go`
- `platform/go.mod`, `platform/go.sum`
- `services/gateway/internal/app/app.go`

**Estimated scope:** M

## Task 3: OTel-логи в order, user, product, notification

**Description:** Повторить строку из Task 2 в `app.go` четырёх сервисов.

**Acceptance criteria:**
- [ ] `SELECT DISTINCT ServiceName FROM otel.otel_logs` — все 5 сервисов
- [ ] С `OTEL_ENABLED=false` все сервисы логируют только в stdout, без ошибок

**Verification:**
- [ ] `task lint`
- [ ] Запуск всех 5 сервисов в двух режимах, SQL выше

**Dependencies:** Task 2

**Files likely touched:**
- `services/{order,user,product,notification}/internal/app/app.go`

**Estimated scope:** S (4 однотипные правки)

## Checkpoint A: логи
- [ ] `task -d platform test`/`lint` зелёные, покрытие `platform/logger` ≥ 80%
- [ ] Все 5 сервисов в `otel.otel_logs`; `SeverityText` соответствует уровню
- [ ] Ревью с пользователем

## Task 4: gRPC server и client (`otelgrpc`)

**Description:** `grpcserver.New` первым добавляет `grpc.StatsHandler(otelgrpc.NewServerHandler(...))`
с фильтром `grpc.reflection.*` и `grpc.health.*`. `grpcclient.New` добавляет
`grpc.WithStatsHandler(otelgrpc.NewClientHandler())`. Пользовательские опции идут после
и могут дополнять. Метрики длительности `otelgrpc` появляются автоматически.

**Acceptance criteria:**
- [ ] bufconn-тест: client-span — родитель server-span, один TraceID; вызов reflection не создаёт спанов
- [ ] Хендлер с `codes.Internal` → server-span `Error`; `codes.NotFound` → по semconv otelgrpc
- [ ] ManualReader: есть метрика длительности RPC с `rpc.service`, `rpc.method`, `rpc.grpc.status_code`

**Verification:**
- [ ] `task -d platform test`, `task -d platform lint`
- [ ] E2E: grpcui → order `CreateOrder` → в `otel.otel_traces` спаны order (server, client) + user/product (server) с одним `TraceId`

**Dependencies:** Task 0

**Files likely touched:**
- `platform/grpc/grpcserver/server.go`, `platform/grpc/grpcclient/client.go`
- `platform/grpc/otel_test.go` (или `grpcserver/server_test.go`)
- `platform/go.mod`, `platform/go.sum`

**Estimated scope:** M

## Task 5: HTTP gateway (`otelhttp`, route-middleware, `request.id`)

**Description:** `httpserver.New` оборачивает роутер в `otelhttp.NewHandler` с фильтром `/swagger/`
и регистрирует `middleware.Route` первым в chi. После `next` Route ставит `span.SetName("METHOD pattern")`,
атрибут `http.route` и `http.route` в `otelhttp.Labeler`. `middleware.RequestID` добавляет
`request.id` в активный span.

**Acceptance criteria:**
- [ ] Unit (chi-маршрут `/api/v1/orders/{id}`, запрос `/api/v1/orders/42`): имя спана `GET /api/v1/orders/{id}`, `http.route` — шаблон в спане и в метрике `http.server.request.duration`
- [ ] `/swagger/index.html` — без спана; ответ 500 → `Error`, 404 — нет; входящий `traceparent` продолжает трейс; `request.id` в спане
- [ ] E2E: `POST /api/v1/orders` → один `TraceId` с корнем в gateway и детьми order/user/product

**Verification:**
- [ ] `task -d platform test` (`httptest` + `tracetest.InMemoryExporter` + `sdkmetric.NewManualReader`)
- [ ] `task -d platform lint`, `task -d services/gateway lint`

**Dependencies:** Task 4

**Files likely touched:**
- `platform/http/httpserver/server.go`
- `platform/http/middleware/route.go`, `platform/http/middleware/request_id.go`
- `platform/http/middleware/route_test.go`

**Estimated scope:** M

## Task 6: Postgres (`otelpgx` tracer + `RecordStats`)

**Description:** `postgres.New` выставляет `poolCfg.ConnConfig.Tracer = otelpgx.NewTracer(...)` без
аргументов запроса. После успешного Ping вызывает `otelpgx.RecordStats(pool)`, при ошибке закрывает
пул (Code Style из SPEC-metrics).

**Acceptance criteria:**
- [ ] SQL-спаны с `db.query.text` без значений параметров — дети gRPC server-спанов order/user/product
- [ ] Запросы внутри `TxManager.WithinTx` видны в трейсе под родительским спаном
- [ ] Метрики пула (acquired/idle/total/max, acquire wait) для order/user/product/notification

**Verification:**
- [ ] `task -d platform lint`, `go build ./...` во всех модулях
- [ ] Ручная проверка на локальном Postgres (интеграционные тесты/testcontainers — Ask first): SQL к `otel_traces` и `otel_metrics_*`

**Dependencies:** Task 5

**Files likely touched:**
- `platform/postgres/postgres.go`
- `platform/go.mod`, `platform/go.sum`

**Estimated scope:** S

## Checkpoint B: сквозной трейс
- [ ] Успешное создание заказа — один `TraceId`: gateway HTTP → gateway gRPC client → order server → order pg + user/product gRPC → их pg; без «осиротевших» корней
- [ ] Grafana (Query type Traces) открывает трейс водопадом, SQL-спаны под gRPC
- [ ] Несуществующий user (`X-User-Id` с фиктивным UUID) → `StatusCode = Error` в order и gateway
- [ ] grpcui (reflection) не создаёт спанов
- [ ] Логи gateway и order по одному запросу — одинаковый непустой `TraceId`; в stdout `trace_id`/`span_id` рядом с `request_id`
- [ ] Ревью с пользователем

## Task 7: Kafka consumer (`kotel` tracer + meter, спан `process`)

**Description:** `kafkaconsumer.New` создаёт `kotel.NewKotel(kotel.WithTracer(...), kotel.WithMeter(...))`
и добавляет `kgo.WithHooks(...)`. Обработка записи выносится в `processRecord`: спан через
`tracer.WithProcessSpan(record)`, ошибка → `RecordError` + `codes.Error`, `defer span.End()`.
Логика commit/rewind в `process` не меняется.

**Acceptance criteria:**
- [ ] Unit без брокера: на каждую запись один спан `process` с `messaging.destination.name`; ошибка хендлера → `Error`; ctx хендлера содержит этот спан
- [ ] E2E: созданный заказ → в notification спан `process` с `messaging.destination.name = order.events.v1` (новый корень — связка с order в `async-propagation`)
- [ ] Метрики `kotel` (records/bytes consumed) с `ServiceName='notification'`

**Verification:**
- [ ] `task -d platform test`, `task -d platform lint`, `task -d services/notification lint`

**Dependencies:** Task 0

**Files likely touched:**
- `platform/kafka/kafkaconsumer/kafkaconsumer.go`
- `platform/kafka/kafkaconsumer/kafkaconsumer_test.go`
- `platform/go.mod`, `platform/go.sum`

**Estimated scope:** M

## Task 8: Runtime-метрики Go

**Description:** `platform/telemetry/runtime.go`: при `Enabled=true` `telemetry.New` после создания
MeterProvider запускает `runtime.Start(runtime.WithMeterProvider(mp))`. При `Enabled=false` ничего не делает.

**Acceptance criteria:**
- [ ] ManualReader: при `Enabled=true` есть goroutines/heap/GC-метрики, при `false` runtime не стартует
- [ ] Покрытие `platform/telemetry` остаётся ≥ 80%
- [ ] E2E: `SELECT DISTINCT ServiceName FROM otel.otel_metrics_gauge` (или `_sum`) с runtime-метриками — все 5 сервисов

**Verification:**
- [ ] `task -d platform test`, `cd platform && go test -cover ./telemetry/...`, `task -d platform lint`

**Dependencies:** Task 0

**Files likely touched:**
- `platform/telemetry/runtime.go`, `platform/telemetry/telemetry.go`
- `platform/telemetry/runtime_test.go`

**Estimated scope:** S

## Checkpoint C: инструментация завершена
- [ ] `task lint`, `go build ./...` во всех модулях go.work, `task -d platform test`
- [ ] Все 5 сервисов стартуют и завершаются в обоих режимах; остановка Collector их не роняет

## Task 9: Каталог метрик + проверка кардинальности и temporality

**Description:** Прогнать ~500 запросов на `POST /api/v1/orders` (успешные и ошибочные) через
`ab -n 500 -c 10 -p body.json`, чтобы сгенерировать метрики. Это не нагрузочное тестирование:
RPS и задержки не анализируются. По данным ClickHouse собрать `docs/observability/metrics.md`: имя,
тип, единица, атрибуты, таблица, сервисы. Проверить кардинальность и temporality.

**Acceptance criteria:**
- [ ] Каталог покрывает все метрики из ClickHouse (`SELECT DISTINCT MetricName` по 4 таблицам) и только их
- [ ] `uniqExact(Attributes)` на сервис < 1000; нет атрибутов с id, `request_id`, сырым путём
- [ ] `AggregationTemporality = 1` (delta) у histogram и monotonic sum

**Verification:**
- [ ] SQL из SPEC-metrics Commands; результаты запросов приложены в описание коммита/PR

**Dependencies:** Task 3, Task 6, Task 7, Task 8

**Files likely touched:**
- `docs/observability/metrics.md`

**Estimated scope:** S

## Task 10: Карта, TODO, Success Criteria трёх спеков

**Description:** Карта: `logs`, `tracing`, `metrics` → done. TODO.md — подпункты прогресса Observability.
Отметить Success Criteria трёх спеков. Формулировки про `/api/v1/orders/{id}` заменить на `POST /api/v1/orders` + unit-тест шаблона (решение R2).

**Acceptance criteria:**
- [ ] Все Success Criteria трёх спеков отмечены с фактическими результатами
- [ ] Карта и TODO.md отражают статус

**Verification:**
- [ ] Ссылки в спеках/карте открываются; `git diff` — только документация

**Dependencies:** Task 9

**Files likely touched:**
- `spec/SPEC-observability-map.md`, `spec/SPEC-logs.md`, `spec/SPEC-tracing.md`, `spec/SPEC-metrics.md`, `TODO.md`

**Estimated scope:** S

## Checkpoint D: модули завершены
- [ ] Все Success Criteria трёх спеков отмечены
- [ ] Ревью с пользователем → переход к `async-propagation`
