# Spec: tracing

> Модуль карты [SPEC-observability-map.md](SPEC-observability-map.md). Зависит от `telemetry-sdk`.

## Objective

Сквозные трейсы синхронного пути `client → gateway (HTTP) → order (gRPC) → user/product (gRPC) → Postgres`
и спаны обработки Kafka-сообщений в notification — за счёт автоинструментации в пакетах `platform`,
без ручных спанов в бизнес-коде.

Инструментация встраивается **в `platform` по умолчанию** и использует глобальные провайдеры: при
`OTEL_ENABLED=false` они noop, поэтому отдельный флаг в пакетах не нужен.

Побочный эффект: те же обёртки (`otelhttp`, `otelgrpc`, `otelpgx`, `kotel`) порождают и метрики —
их доведение (атрибуты, pool stats, runtime) — модуль `metrics`.

### Acceptance criteria

1. **HTTP (gateway):** `httpserver` оборачивает роутер в `otelhttp.NewHandler`; имя server-спана — `METHOD route-pattern`
   (`GET /api/v1/orders/{id}`), атрибут `http.route` выставлен. Запросы к `/swagger/*` не трассируются.
   Входящий `traceparent` от клиента продолжает трейс.
2. **gRPC сервер (order/user/product):** `grpcserver` добавляет `grpc.StatsHandler(otelgrpc.NewServerHandler(...))`;
   методы `grpc.reflection.*` и `grpc.health.*` отфильтрованы.
3. **gRPC клиент (gateway→order, order→user/product):** `grpcclient` добавляет `grpc.WithStatsHandler(otelgrpc.NewClientHandler())`; контекст пропагируется в метаданных.
4. **Postgres (все сервисы с БД):** `postgres.New` выставляет `ConnConfig.Tracer = otelpgx.NewTracer(...)`;
   спаны запросов с `db.query.text` (параметры **не** включаются), транзакции `postgres.TxManager` видны как спаны запросов внутри родителя.
5. **Kafka consumer (notification):** `kafkaconsumer` подключает `kotel`-хуки; каждая запись обрабатывается
   в собственном спане `process` (`messaging.*` атрибуты), `ctx` хендлера — контекст этого спана.
   Без `traceparent` в заголовках — новый корневой трейс (связку с order делает `async-propagation`).
6. **Ошибки:** если хендлер вернул ошибку, span получает `status=Error` и событие `exception`
   (gRPC — по коду, HTTP — 5xx; 4xx на сервере не помечаются ошибкой по semconv).
7. **request_id ↔ trace:** middleware `RequestID` добавляет атрибут `request.id` в активный span.

## Tech Stack

| Модуль | Версия |
|---|---|
| `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` | `v0.72.0` |
| `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc` | `v0.72.0` |
| `github.com/exaring/otelpgx` | `v0.13.0` |
| `github.com/twmb/franz-go/plugin/kotel` | `v1.7.1` |

## Commands

```bash
task -d platform test
task watch-all
curl -s -X POST localhost:<gateway>/api/v1/orders -d '...'   # создать заказ
# Grafana Explore → SQL Editor: SELECT ServiceName, SpanName, ParentSpanId FROM otel.otel_traces WHERE TraceId = '...' ORDER BY Timestamp
```

## Project Structure

```
platform/http/httpserver/server.go       → otelhttp.NewHandler вокруг router, фильтр /swagger
platform/http/middleware/route.go        → имя спана и http.route из chi.RouteContext после routing
platform/http/middleware/request_id.go   → + request.id в span
platform/grpc/grpcserver/server.go       → StatsHandler по умолчанию, фильтр reflection/health
platform/grpc/grpcclient/client.go       → WithStatsHandler по умолчанию
platform/postgres/postgres.go            → otelpgx tracer
platform/kafka/kafkaconsumer/            → kotel hooks + processing span per record
platform/**/..._test.go                  → unit-тесты
```

Изменений в `services/*` не ожидается (кроме, возможно, порядка middleware в gateway).

## Code Style

```go
// grpcserver.New
srv.server = grpc.NewServer(append([]grpc.ServerOption{
	grpc.StatsHandler(otelgrpc.NewServerHandler(
		otelgrpc.WithFilter(filters.Not(filters.ServicePrefix("grpc.reflection"))),
	)),
}, srv.serverOpts...)...)
```

```go
// kafkaconsumer.Run — обработка записи
ctx, span := c.tracer.WithProcessingSpan(record)
err := handler.Handle(ctx, toMessage(record))
if err != nil {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
span.End()
```

Соглашения: имена спанов — по semconv (не придумываем свои); бизнес-код спанов не создаёт.
Ручные спаны в use case — вне скоупа (см. Ask first).

## Testing Strategy

| Уровень | Что | Как |
|---|---|---|
| Unit | HTTP: имя спана = route pattern, `/swagger/*` без спана, 5xx → Error | `httptest` + `tracetest.InMemoryExporter`, `otel.SetTracerProvider` в тесте |
| Unit | gRPC: server+client через `bufconn` → client-span родитель server-span; reflection отфильтрован | `google.golang.org/grpc/test/bufconn` + InMemoryExporter |
| Unit | kafkaconsumer: span на каждую запись, ошибка хендлера → Error status | вынести обработку записи в функцию, тестировать без брокера |
| Integration | otelpgx: спан на `Exec` без параметров | ручная проверка на локальном Postgres (testcontainers — Ask first) |
| E2E | создание заказа → один трейс: gateway HTTP → gateway gRPC client → order server → order pg + user/product gRPC → их pg | Grafana Explore → SQL Editor + Grafana Traces |

## Boundaries

- **Always:** инструментация через `platform`, сервисы её не дублируют; параметры SQL не пишутся в спаны.
- **Ask first:** ручные спаны в use case/репозиториях; testcontainers для интеграционных тестов; трассировка тел запросов/ответов.
- **Never:** класть PII (адреса доставки, user payload) в атрибуты спанов; генерировать `http.route` из сырого пути (кардинальность).

## Success Criteria

- [ ] `task -d platform test` зелёный.
- [ ] Создание заказа даёт в `otel_traces` один `TraceId` со спанами от `gateway`, `order`, `user`, `product`; иерархия корректна (нет «осиротевших» корней, кроме ожидаемого корня в gateway).
- [ ] В Grafana (Query type Traces) трейс заказа открывается водопадом, SQL-спаны видны под gRPC-спанами.
- [x] Обработка события в notification даёт спан `process` с `messaging.source.name = order.events.v1` (`kotel` v1.7.1 пишет semconv v1.18, атрибута `messaging.destination.name` в нём нет).
- [ ] Ошибка (несуществующий user) → спаны с `StatusCode = Error` в order и gateway.
- [ ] Вызовы grpcui (reflection) не создают спанов.

## Open Questions

Нет.
