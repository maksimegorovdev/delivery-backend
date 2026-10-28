# Implementation Plan: async-propagation

> Спек: [SPEC-async-propagation.md](../spec/SPEC-async-propagation.md) · Карта: [SPEC-observability-map.md](../spec/SPEC-observability-map.md)
> Задачи: [todo.md](todo.md) · Предыдущий модуль: [archive/logs-tracing-metrics-plan.md](archive/logs-tracing-metrics-plan.md)

## Overview

Трейс создания заказа продолжается в notification: order кладёт W3C trace context в строку
`outbox_events`, Debezium EventRouter переносит его в заголовки Kafka-сообщения, а `kotel` в notification
продолжает трейс. Код меняется только в order: миграция `000003` и `OutboxRepo.Save`. Также меняется конфиг
коннектора. Notification и `platform/kafka` не трогаем, потому что консьюмер уже готов принять контекст.

## Что уже есть (проверено в коде)

- `platform/telemetry` ставит глобальный пропагатор `TraceContext + Baggage`.
- `kotel.NewTracer` без `TracerPropagator` берёт `otel.GetTextMapPropagator()` и извлекает контекст из
  заголовков записи (`kotel/tracer.go:91,289`). `processRecord` создаёт `process` дочерним к `receive`
  (`platform/kafka/kafkaconsumer/kafkaconsumer.go:160`). AC4 и AC6 на стороне консьюмера закрываются
  без изменений кода, нужна только e2e-проверка.
- `OutboxRepo.Save` вызывается в `WithinTx` из `service.CreateOrder`. В `ctx` лежит серверный
  gRPC-спан order (otelpgx не кладёт спан запроса в ctx вызывающего). Значит родителем `receive`
  станет спан `order.v1.OrderService/CreateOrder`, а SQL-спан `INSERT outbox_events` будет ему «братом».
  Для AC4 этого достаточно: это спан order, в рамках которого сделан INSERT.

## Architecture Decisions

- **Срез 1 — инфраструктурный путь без Go-кода.** Миграция и коннектор проверяются ручным `INSERT`
  с известным `traceparent`. Это выносит главный риск (как Debezium кодирует заголовок и как ведёт
  себя с NULL) в начало, до правок Go.
- **Построение аргументов выносится в функцию `traceContext(ctx, propagator) (traceparent, tracestate *string)`.**
  `Save` передаёт `otel.GetTextMapPropagator()`, а unit-тест — `propagation.TraceContext{}`.
  Тест не мутирует глобальное состояние и может идти с `t.Parallel()`. Пустое значение превращается в `nil`,
  то есть SQL `NULL`. Это `nullIfEmpty` из спека, свёрнутый внутрь функции.
- **`baggage` не сохраняется.** Композитный пропагатор кладёт в carrier и `baggage`. Спек сохраняет
  только `traceparent`/`tracestate`, а gateway входящий baggage и так не доверяет. Лишний ключ carrier'а игнорируется.
- **`migrate-down` в Taskfile откатывает все миграции.** Откат `000003` проверяем через
  `bin/migrate ... down 1`, а не `task migrate-down`, иначе снесутся таблицы заказов.
- **`go.opentelemetry.io/otel` в order становится прямой зависимостью** (сейчас `// indirect`):
  нужен `go mod tidy`/`task tidy`. Новых модулей не появляется.

## Dependency Graph

```
Task 1: миграция 000003 + коннектор (ручной INSERT → заголовки → notification)
    │
    └── Task 2: OutboxRepo.Save инжектит контекст + unit-тест → e2e сквозной трейс
            │
            └── Task 3: Success Criteria, карта, TODO
```

Задачи строго последовательны: колонок до Task 1 нет, а e2e Task 2 опирается на коннектор.

## Task List

### Phase 1: Инфраструктурный путь
- [x] Task 1: Миграция `000003` + `additional.placement` в коннекторе

### Checkpoint A: заголовки доходят до notification

### Phase 2: Инжект в order
- [ ] Task 2: `OutboxRepo.Save` пишет `traceparent`/`tracestate`

### Checkpoint B: сквозной трейс gateway → notification

### Phase 3: Закрытие модуля
- [ ] Task 3: Success Criteria спека, карта, TODO.md

### Checkpoint C: модуль завершён

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Debezium кладёт значение заголовка не сырыми байтами (например, JSON-строкой в кавычках), и `kotel` не распарсит `traceparent` | High | Task 1 проверяет байты заголовка в Redpanda Console/`rpk` до Go-кода. Если формат не тот — задать `header.converter` (`SimpleHeaderConverter`/`StringConverter`) в конфиге коннектора; вопрос к пользователю, если нужен иной путь |
| Колонка `NULL` даёт заголовок с null-значением или ломает EventRouter | Med | Task 1 проверяет ручной INSERT без `traceparent`: сообщение доходит, notification обрабатывает его новым корнем (AC5) |
| Изменение конфига коннектора перезапускает задачу и пропускает/дублирует события | Low | `PUT /config` сохраняет offset'ы коннектора. Идемпотентности в notification пока нет (отдельный пункт TODO.md), но дубли на dev-стенде допустимы. Проверяем статус `RUNNING` |
| `task migrate-down` откатывает всё | Med | Откат только `down 1` напрямую через `bin/migrate` |
| Пробел на водопаде длиной в задержку CDC | Low | Осознанный компромисс из спека, OTel Java agent — Ask first |

## Open Questions

- Нет блокирующих. Если в Task 1 выяснится, что нужен `header.converter`, это правка того же
  `order-outbox.json` в рамках спека. Если нужен interceptor в Debezium — Ask first.
