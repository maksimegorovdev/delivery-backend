# Spec: metrics

> Status: **done** · Depends on: [telemetry-sdk](../telemetry-sdk/spec.md)
> Использует обёртки, подключённые в `tracing` (они эмитят и метрики); порядок `tracing` → `metrics` удобен, но не обязателен.

## Objective

Технические и RED-метрики (Rate, Errors, Duration) для всех сервисов в ClickHouse (`otel.otel_metrics_*`),
чтобы в модуле `dashboards` построить обзор сервисов. Бизнес-метрики — вне скоупа (следующая итерация).

### Acceptance criteria

1. **HTTP (gateway):** `http.server.request.duration` (histogram, s) с атрибутами `http.request.method`,
   `http.route` (шаблон chi, не сырой путь), `http.response.status_code`.
2. **gRPC (server и client):** метрики длительности вызовов `otelgrpc` с `rpc.service`, `rpc.method`, `rpc.grpc.status_code`.
3. **Postgres:** длительность запросов от `otelpgx` + статистика пула `pgxpool` (acquired/idle/total/max conns, acquire wait)
   через `otelpgx.RecordStats(pool)`.
4. **Kafka (notification):** метрики `kotel` meter (records/bytes consumed по топику).
5. **Runtime Go (все сервисы):** `go.opentelemetry.io/contrib/instrumentation/runtime` — goroutines, heap, GC, memory limit.
6. Кардинальность: ни один атрибут метрик не содержит идентификаторов (user/order id, request_id, сырой URL).
   Итоговое число уникальных time series на сервис < 1000 при локальной нагрузке.
7. Counter/histogram приходят в ClickHouse как **delta** (`AggregationTemporality = 1` в `otel_metrics_sum`/`otel_metrics_histogram`).
8. Каталог метрик (имя, тип, единица, атрибуты, таблица ClickHouse) зафиксирован в `spec/specs/observability/metrics/metrics-catalog.md` — это вход для `dashboards`.

## Tech Stack

| Модуль | Версия |
|---|---|
| `go.opentelemetry.io/contrib/instrumentation/runtime` | `v0.72.0` |
| `otelhttp`, `otelgrpc`, `otelpgx`, `kotel` | как в [spec tracing](../tracing/spec.md) |

## Commands

```bash
task -d platform test
task watch-all
hey -n 500 -c 10 http://localhost:<gateway>/api/v1/orders/<id>   # или curl в цикле — нагрузка для проверки
# Grafana Explore → SQL Editor:
#   SELECT MetricName, count(), uniqExact(Attributes) FROM otel.otel_metrics_histogram GROUP BY MetricName
#   SELECT ServiceName, MetricName FROM otel.otel_metrics_gauge GROUP BY ALL
```

## Project Structure

```
platform/telemetry/runtime.go             → запуск runtime-метрик в telemetry.New (если Enabled)
platform/http/middleware/route.go         → http.route в метрики через otelhttp.Labeler (общий с tracing)
platform/postgres/postgres.go             → otelpgx.RecordStats(pool)
platform/kafka/kafkaconsumer/             → kotel meter
spec/specs/observability/metrics/metrics-catalog.md     → каталог метрик
```

## Code Style

```go
// postgres.New, после успешного Ping
if err := otelpgx.RecordStats(pool); err != nil {
	pool.Close()
	return nil, fmt.Errorf("postgres: record stats: %w", err)
}
```

```go
// route middleware: шаблон маршрута — в метрики и в span
next.ServeHTTP(w, r)
if pattern := chi.RouteContext(r.Context()).RoutePattern(); pattern != "" {
	if labeler, ok := otelhttp.LabelerFromContext(r.Context()); ok {
		labeler.Add(semconv.HTTPRoute(pattern))
	}
}
```

Соглашения: только semconv-имена метрик из библиотек; собственных метрик в этом модуле нет.

## Testing Strategy

| Уровень | Что | Как |
|---|---|---|
| Unit | HTTP-метрика содержит `http.route` = шаблон, а не путь с id | `sdkmetric.NewManualReader` + `httptest` + chi |
| Unit | runtime-метрики регистрируются только при `Enabled` | ManualReader |
| Unit | temporality delta для histogram | ManualReader с selector'ом из `telemetry` |
| E2E | после нагрузки все метрики из каталога есть в ClickHouse; `uniqExact(Attributes)` в разумных пределах | Grafana Explore → SQL Editor |

## Boundaries

- **Always:** сверять каталог `spec/specs/observability/metrics/metrics-catalog.md` с реальными данными в ClickHouse; проверять кардинальность новых атрибутов.
- **Ask first:** бизнес-метрики; exemplars; добавление Prometheus-эндпоинта; изменение интервала экспорта < 10s.
- **Never:** идентификаторы и сырые пути в атрибутах метрик.

## Success Criteria

- [x] `task -d platform test` зелёный.
- [x] Для всех 5 сервисов в ClickHouse есть runtime-метрики (`go.*` лежат в `otel_metrics_sum`, а не в `gauge`; `go.goroutine.count` — gateway, notification, order, product, user).
- [x] `http.server.request.duration` с шаблонным `http.route`: в ClickHouse — `POST /api/v1/orders` (`http.route = /api/v1/orders`); шаблон с параметром `/api/v1/orders/{id}` проверен unit-тестом на chi-маршруте (решение R2: `GET /api/v1/orders/{id}` в gateway нет).
- [x] gRPC-метрики длительности для order/user/product (server) и gateway/order (client): `rpc.server.call.duration`, `rpc.client.call.duration`.
- [x] Метрики пула pgx для order/user/product/notification (`pgxpool.*`, `db.client.operation.duration`).
- [x] `AggregationTemporality` = delta для histogram/sum-counter (`1`; non-monotonic sum — cumulative `2`).
- [x] `spec/specs/observability/metrics/metrics-catalog.md` заполнен и соответствует ClickHouse (gauge 1, sum 26, histogram 6).

## Open Questions

Нет.
