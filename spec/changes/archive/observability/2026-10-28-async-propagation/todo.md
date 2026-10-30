# Tasks: async-propagation

> План: [plan.md](plan.md) · Спек: [spec.md](spec.md)

E2E-запросы к ClickHouse: `docker exec clickhouse clickhouse-client -q "<SQL>"` или Grafana Explore → SQL Editor.
Заголовки Kafka: Redpanda Console http://localhost:3000 → topic `order.events.v1` → Headers.

## Task 1: Миграция `000003` + `additional.placement` в коннекторе

**Description:** Добавить в `outbox_events` nullable-колонки `traceparent TEXT`, `tracestate TEXT`
(up/down). Дополнить `transforms.outbox.table.fields.additional.placement` в `deploy/debezium/order-outbox.json`
значением `traceparent:header:traceparent,tracestate:header:tracestate`, сохранив `type:header:event-type`.
Проверить путь без Go-кода: ручной `INSERT` в `outbox_events` с известным `traceparent` → заголовок
в Kafka → notification продолжает этот трейс. Ручной `INSERT` без `traceparent` → новый корень без ошибок.

**Acceptance criteria:**
- [x] `000003_add_outbox_trace_context.{up,down}.sql` применяется (`task -d services/order migrate-up`) и откатывается (`bin/migrate ... down 1`); `000001`/`000002` не тронуты
- [x] После `task connect-register` коннектор `RUNNING`; сообщение от ручного INSERT содержит заголовки `event-type` и `traceparent`, значение `traceparent` — сырая строка `00-<trace>-<span>-01` (без JSON-кавычек)
- [x] В `otel.otel_traces` спан `receive` notification имеет `TraceId` и `ParentSpanId` из вставленного `traceparent`, `process` — дочерний к `receive`; INSERT без `traceparent` даёт новый корневой трейс, в логах notification нет ошибок обработки

**Verification:**
- [x] `\d outbox_events` в order-postgres показывает две nullable-колонки; после `down 1` — исчезают; повторный `up` — снова есть
- [x] `curl -s localhost:8083/connectors/order-outbox/status` → `RUNNING`, у task нет `trace`
- [x] SQL `SELECT ServiceName, SpanName, SpanId, ParentSpanId FROM otel.otel_traces WHERE TraceId = '<trace>'`

**Dependencies:** None

**Files likely touched:**
- `services/order/migrations/pg/000003_add_outbox_trace_context.up.sql`
- `services/order/migrations/pg/000003_add_outbox_trace_context.down.sql`
- `deploy/debezium/order-outbox.json`

**Estimated scope:** S

## Checkpoint A: заголовки доходят до notification
- [x] Формат заголовка подтверждён (иначе — решение с `header.converter` согласовано с пользователем)
- [x] Ревью с пользователем

## Task 2: `OutboxRepo.Save` пишет `traceparent`/`tracestate`

**Description:** В `services/order/internal/repository/pg/outbox.go` добавить функцию
`traceContext(ctx, propagator) (traceparent, tracestate *string)`: `Inject` в `propagation.MapCarrier`,
пустое значение превращается в `nil`. `Save` вызывает её с `otel.GetTextMapPropagator()` и пишет обе колонки.
Домен (`domain.OutboxEvent`, `OrderEventBuilder`) не меняется. Unit-тест в `outbox_test.go` (package `pg`),
table-driven, без глобального пропагатора.

**Acceptance criteria:**
- [x] При активном span в ctx `traceparent` = `00-<TraceID>-<SpanID>-<flags>` этого span'а, `tracestate` передаётся при наличии и `nil` при отсутствии; без span оба `nil`
- [x] Создание заказа через gateway: один `TraceId` у спанов gateway, order, user, product и notification; `ParentSpanId` у `receive` = `SpanId` серверного спана order `CreateOrder`
- [x] С `OTEL_ENABLED=false` в order заказ создаётся, колонки `NULL`, notification обрабатывает событие новым корнем (AC5)

**Verification:**
- [x] `cd services/order && go test -race ./...` зелёный; `task -d services/order lint` зелёный; `task -d services/order tidy` — `otel` стал прямой зависимостью, других изменений go.mod нет
- [x] `go build ./...` во всех модулях go.work
- [x] E2E: `POST /api/v1/orders` → `SELECT traceparent FROM outbox_events ORDER BY created_at DESC LIMIT 1` не NULL; SQL по `TraceId` в `otel.otel_traces` и `otel.otel_logs` (логи notification с тем же `TraceId`, AC6 — не проверено: notification не пишет логов при обработке события, в `otel_logs` только `kafka consumer started/stopped`)

**Dependencies:** Task 1

**Files likely touched:**
- `services/order/internal/repository/pg/outbox.go`
- `services/order/internal/repository/pg/outbox_test.go`
- `services/order/go.mod`, `services/order/go.sum`

**Estimated scope:** S

## Checkpoint B: сквозной трейс gateway → notification
- [x] Grafana (Query type Traces) показывает водопад от gateway до notification `process` с пробелом на CDC
- [x] Ревью с пользователем

## Task 3: Success Criteria спека, карта, TODO.md

**Description:** Отметить Success Criteria `SPEC-async-propagation.md` с фактическими результатами
(TraceId, формат заголовка). В карте `async-propagation` → done. В `TODO.md` отметить подпункт async-propagation.

**Acceptance criteria:**
- [x] Все 5 Success Criteria спека отмечены с фактами (логи notification — через ошибку хендлера)
- [x] Карта и TODO.md отражают статус

**Verification:**
- [x] `git diff` — только документация; ссылки открываются

**Dependencies:** Task 2

**Files likely touched:**
- `spec/changes/archive/observability/2026-10-28-async-propagation/spec.md`, `spec/specs/observability/map.md`, `TODO.md`

**Estimated scope:** XS

## Checkpoint C: модуль завершён
- [x] Success Criteria спека отмечены
- [x] Ревью с пользователем → переход к `dashboards`
