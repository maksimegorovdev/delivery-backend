# Каталог метрик

Снимок по данным ClickHouse (`otel.otel_metrics_*`) после нагрузки: `ab -n 500 -c 10` на успешный
`POST /api/v1/orders` и `ab -n 100 -c 5` на ошибочный (несуществующий продукт → 404). Это генерация
метрик, а не нагрузочное тестирование: RPS и задержки не анализировались.

Сервисы: `gateway`, `order`, `user`, `product`, `notification`. Экспорт — OTLP gRPC в Collector,
temporality: counter/histogram — **delta** (`AggregationTemporality = 1`), non-monotonic sum
(UpDownCounter и async-gauge) — **cumulative** (`2`).

## Runtime Go

Источник: `go.opentelemetry.io/contrib/instrumentation/runtime`. Таблица: `otel_metrics_sum`. Все 5 сервисов.

| Метрика | Тип | Единица | Атрибуты | Описание |
|---|---|---|---|---|
| `go.config.gogc` | sum, non-monotonic | `%` | — | Целевой процент роста кучи (GOGC) |
| `go.goroutine.count` | sum, non-monotonic | `{goroutine}` | — | Число живых горутин |
| `go.memory.allocated` | sum, monotonic | `By` | — | Память, выделенная в куче приложением |
| `go.memory.allocations` | sum, monotonic | `{allocation}` | — | Число аллокаций в куче |
| `go.memory.gc.goal` | sum, non-monotonic | `By` | — | Целевой размер кучи к концу цикла GC |
| `go.memory.used` | sum, non-monotonic | `By` | `go.memory.type` | Память, используемая рантаймом |
| `go.processor.limit` | sum, non-monotonic | `{thread}` | — | Число потоков ОС, исполняющих Go-код (GOMAXPROCS) |

## HTTP (gateway)

Источник: `otelhttp`. Таблица: `otel_metrics_histogram`. Сервис: `gateway`.

Общие атрибуты: `http.request.method`, `http.response.status_code`, `http.route`, `network.protocol.name`,
`network.protocol.version`, `server.address`, `server.port`, `url.scheme`.
`http.route` — шаблон роутера (`/api/v1/orders`), выставляется `middleware.Route`, а не сырой путь.

| Метрика | Тип | Единица | Описание |
|---|---|---|---|
| `http.server.request.duration` | histogram | `s` | Длительность HTTP-запросов |
| `http.server.request.body.size` | histogram | `By` | Размер тела запроса |
| `http.server.response.body.size` | histogram | `By` | Размер тела ответа |

## gRPC

Источник: `otelgrpc`. Таблица: `otel_metrics_histogram`. Атрибуты фильтруются: reflection и health не пишутся.

| Метрика | Тип | Единица | Атрибуты | Сервисы |
|---|---|---|---|---|
| `rpc.client.call.duration` | histogram | `s` | `rpc.method` (`service/method`), `rpc.response.status_code`, `rpc.system.name`, `error.type`, `server.address`, `server.port` | `gateway`, `order` |
| `rpc.server.call.duration` | histogram | `s` | `rpc.method`, `rpc.response.status_code`, `rpc.system.name` | `order`, `user`, `product` |

## Postgres

Источник: `otelpgx` (`db.client.operation.duration`) и `otelpgx.RecordStats` (`pgxpool.*`).
Сервисы: `order`, `user`, `product`, `notification` (у `gateway` БД нет).

| Метрика | Таблица | Тип | Единица | Атрибуты |
|---|---|---|---|---|
| `db.client.operation.duration` | histogram | histogram | `s` | `db.system.name`, `pgx.operation.type` (`connect`/`acquire`/`copy`/`prepare`/`query`) |
| `pgxpool.max_connections` | gauge | gauge | — | `db.client.connection.pool.name`, `db.system.name` |
| `pgxpool.acquire_duration` | sum | monotonic | `ns` | то же |
| `pgxpool.acquires` | sum | monotonic | — | то же |
| `pgxpool.canceled_acquires` | sum | monotonic | — | то же |
| `pgxpool.empty_acquire` | sum | monotonic | — | то же |
| `pgxpool.empty_acquire_wait_time` | sum | monotonic | `ns` | то же |
| `pgxpool.max_idle_destroys` | sum | monotonic | — | то же |
| `pgxpool.max_lifetime_destroys` | sum | monotonic | — | то же |
| `pgxpool.new_connections` | sum | monotonic | — | то же |
| `pgxpool.acquired_connections` | sum | non-monotonic | — | то же |
| `pgxpool.constructing_connections` | sum | non-monotonic | — | то же |
| `pgxpool.idle_connections` | sum | non-monotonic | — | то же |
| `pgxpool.total_connections` | sum | non-monotonic | — | то же |

SQL-текст в атрибуты метрик не попадает.

## Kafka (notification)

Источник: `kotel` meter. Таблица: `otel_metrics_sum`, все метрики monotonic. Сервис: `notification`.
`node_id` — идентификатор брокера (`0`, `seed_0`), значений единицы.

| Метрика | Единица | Атрибуты | Описание |
|---|---|---|---|
| `messaging.kafka.connects.count` | `1` | `node_id` | Открытые соединения |
| `messaging.kafka.disconnects.count` | `1` | `node_id` | Закрытые соединения |
| `messaging.kafka.fetch_bytes.count` | `by` | `node_id`, `topic` | Несжатые байты из fetch |
| `messaging.kafka.fetch_records.count` | `1` | `node_id`, `topic` | Записи из fetch |
| `messaging.kafka.read_bytes.count` | `by` | `node_id` | Прочитано байт |
| `messaging.kafka.read_errors.count` | `1` | `node_id` | Ошибки чтения |
| `messaging.kafka.write_bytes` | `by` | `node_id` | Записано байт |

## Проверки

Запросы выполняются в Grafana Explore → SQL Editor или `docker exec clickhouse clickhouse-client -q "<SQL>"`.

**Покрытие каталога:**

```sql
SELECT 'gauge' t, MetricName FROM otel.otel_metrics_gauge GROUP BY MetricName
UNION ALL SELECT 'sum', MetricName FROM otel.otel_metrics_sum GROUP BY MetricName
UNION ALL SELECT 'histogram', MetricName FROM otel.otel_metrics_histogram GROUP BY MetricName
UNION ALL SELECT 'exponential_histogram', MetricName FROM otel.otel_metrics_exponential_histogram GROUP BY MetricName
UNION ALL SELECT 'summary', MetricName FROM otel.otel_metrics_summary GROUP BY MetricName
```

Результат: gauge — 1 (`pgxpool.max_connections`), sum — 26, histogram — 6, остальные таблицы пусты.

**Кардинальность** (`uniqExact(Attributes)` на метрику и сервис):

```sql
SELECT ServiceName, MetricName, uniqExact(Attributes) u
FROM otel.otel_metrics_histogram
GROUP BY ServiceName, MetricName ORDER BY u DESC
```

Максимум на метрику — 5 (`db.client.operation.duration`). Суммарно по всем таблицам на сервис:
`notification` 37, `order` 32, `user` 28, `product` 26, `gateway` 22 — на порядки ниже лимита 1000.
Атрибутов с id, `request_id` и сырым путём нет: `http.route` — только `/api/v1/orders`,
`rpc.method` — только `service/method`.

**Temporality:**

```sql
SELECT MetricName, AggregationTemporality, count()
FROM otel.otel_metrics_histogram GROUP BY ALL
```

Все 6 histogram — `1` (delta). В `otel_metrics_sum` у всех monotonic-метрик — `1` (delta); `2` (cumulative)
только у non-monotonic (`go.goroutine.count`, `go.memory.used`, `pgxpool.idle_connections` и т. п.).
