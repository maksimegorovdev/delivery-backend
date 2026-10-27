# Spec: async-propagation

> Модуль карты [SPEC-observability-map.md](SPEC-observability-map.md). Зависит от `tracing`.

## Objective

Один трейс от HTTP-запроса создания заказа до обработки события в notification:

```
gateway HTTP ─▶ order gRPC ─▶ INSERT outbox_events (traceparent) ─▶ Debezium EventRouter
    ─▶ Kafka header traceparent ─▶ notification process span (child)
```

Debezium не инструментируется (нужен OTel Java agent в Kafka Connect) — между спаном записи в outbox
и спаном обработки будет «пробел» длиной в задержку CDC. Это осознанный компромисс.

### Acceptance criteria

1. Миграция order `000003`: в `outbox_events` добавлены nullable-колонки `traceparent TEXT`, `tracestate TEXT`; down-миграция их удаляет.
2. `OutboxRepo.Save` инжектит текущий trace context из `ctx` (W3C, глобальный пропагатор) в эти колонки.
   Домен (`domain.OutboxEvent`, `OrderEventBuilder`) **не меняется** — tracing остаётся инфраструктурной деталью репозитория.
3. Коннектор `deploy/debezium/order-outbox.json`: `transforms.outbox.table.fields.additional.placement`
   дополнен `traceparent:header:traceparent,tracestate:header:tracestate` (сохраняя `type:header:event-type`).
4. В notification спан `process` (из модуля `tracing`) — дочерний к спану order, сделавшему INSERT:
   совпадает `TraceId`, `ParentSpanId` = span order'а.
5. Событие без trace context (старые записи, `OTEL_ENABLED=false` в order) обрабатывается штатно — новый корневой трейс, без ошибок.
6. Логи notification при обработке события имеют тот же `TraceId` (следствие модуля `logs`).

## Tech Stack

- Существующие: golang-migrate, Debezium `3.6.3.Final` (EventRouter SMT), franz-go + `kotel` (из `tracing`).
- Новых зависимостей нет (`go.opentelemetry.io/otel/propagation` уже есть через SDK).

## Commands

```bash
task -d services/order migrate-create        # 000003_add_outbox_trace_context
task migrate-up
task connect-register                         # PUT обновлённого конфига коннектора
curl -s localhost:8083/connectors/order-outbox/status
# e2e: создать заказ, затем
# Grafana Explore → SQL Editor: SELECT ServiceName, SpanName, SpanId, ParentSpanId FROM otel.otel_traces WHERE TraceId = '...' ORDER BY Timestamp
# заголовки в Kafka: Redpanda Console http://localhost:3000 → topic order.events.v1 → Headers
```

## Project Structure

```
services/order/migrations/pg/000003_add_outbox_trace_context.{up,down}.sql
services/order/internal/repository/pg/outbox.go   → inject traceparent/tracestate
deploy/debezium/order-outbox.json                 → additional.placement
services/order/internal/repository/pg/outbox_test.go (unit на carrier) 
```

## Code Style

```go
func (r *OutboxRepo) Save(ctx context.Context, event domain.OutboxEvent) error {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)

	if _, err := postgres.DB(ctx, r.pool).Exec(
		ctx,
		`INSERT INTO outbox_events (id, aggregate_type, aggregate_id, type, payload, created_at, traceparent, tracestate)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		event.ID, event.AggregateType, event.AggregateID, event.Type, event.Payload, event.CreatedAt,
		nullIfEmpty(carrier.Get("traceparent")), nullIfEmpty(carrier.Get("tracestate")),
	); err != nil {
		return pgerr.Map(err)
	}
	return nil
}
```

## Testing Strategy

| Уровень | Что | Как |
|---|---|---|
| Unit | инжект: при активном span → валидный `traceparent`; без span → NULL | вынести построение аргументов в функцию; `trace.ContextWithSpanContext` |
| Migration | up/down применяются и откатываются чисто | `task -d services/order migrate-up` / `migrate-down` на локальной БД |
| E2E | заказ → в Kafka-сообщении заголовок `traceparent`; в ClickHouse один `TraceId` от gateway до notification | Redpanda Console + Grafana Explore → SQL Editor |
| E2E | событие без заголовка → обработано, новый трейс | INSERT в `outbox_events` вручную без traceparent |

## Boundaries

- **Always:** миграции только новыми файлами; конфиг коннектора применять через `task connect-register`.
- **Ask first:** OTel Java agent / tracing interceptor в Debezium; спан «outbox write» вручную; изменение схемы payload события.
- **Never:** менять применённые миграции `000001`/`000002`; класть trace context в payload события (это контракт для потребителей).

## Success Criteria

- [ ] Миграция `000003` применяется и откатывается.
- [ ] Сообщение в `order.events.v1` содержит заголовки `event-type` и `traceparent`.
- [ ] Трейс создания заказа содержит спаны `gateway`, `order`, `user`, `product` и `notification`; спан notification — потомок спана order.
- [ ] Логи notification, связанные с событием, имеют тот же `TraceId`.
- [ ] Событие без `traceparent` обрабатывается без ошибок.

## Open Questions

Нет.
