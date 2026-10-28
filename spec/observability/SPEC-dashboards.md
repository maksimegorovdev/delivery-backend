# Spec: dashboards

> Модуль карты [SPEC-observability-map.md](SPEC-observability-map.md). Зависит от `logs`, `tracing`, `metrics`.
>
> Уточнён 2026-10-28 по [docs/observability/metrics.md](../../docs/observability/metrics.md), данным в ClickHouse
> и содержимому плагина `grafana-clickhouse-datasource` 4.23.2 в контейнере Grafana.

## Objective

Дашборды Grafana as code (provisioning из `deploy/grafana/dashboards`), чтобы после `task dev` сразу видеть
состояние сервисов и переходить между метриками, логами и трейсами без ручной настройки.

### Acceptance criteria

1. **Готовые OTel-дашборды плагина** (`grafana-clickhouse-datasource` 4.23.2) провиженятся из файлов, а не импортируются кнопкой:
   - OpenTelemetry Logs Explorer (`otel-logs-explorer`);
   - OpenTelemetry Traces Explorer (`otel-traces-explorer`: поиск трейсов, service map);
   - OpenTelemetry Service Dashboard (`otel-service-dashboard`: RED по спанам server/client/internal/async, ошибки,
     медленные операции, логи сервиса).

   JSON берётся из `/var/lib/grafana/plugins/grafana-clickhouse-datasource/dashboards/` образа с плагином 4.23.2.
   File-provisioning не разрешает `__inputs`, поэтому `${DS_GRAFANA_CLICKHOUSE_DATASOURCE}` заменяется на uid `clickhouse`,
   блок `__inputs` удаляется. Других правок в JSON плагина нет; uid дашбордов плагина сохраняются (на них ссылаются
   их собственные data links).
2. **Свой дашборд «Delivery / Services Overview»** (`delivery-services-overview`) на метриках (`otel_metrics_*`).
   Он даёт то, чего нет в Service Dashboard плагина: обзор всех сервисов сразу и метрики, которых нет в спанах.
   - **RED-сводка (HTTP + gRPC server):** по сервису — RPS, error rate, client errors, p95.
     - gateway: `http.server.request.duration`, ошибка — `http.response.status_code >= 500`, клиентская — `4xx`;
       разбивка по `http.route`;
     - order/user/product: `rpc.server.call.duration`, ошибка — серверные коды по semconv (`UNKNOWN`, `DEADLINE_EXCEEDED`,
       `UNIMPLEMENTED`, `INTERNAL`, `UNAVAILABLE`, `DATA_LOSS`), клиентская — остальные коды кроме `OK`
       (`NOT_FOUND`, `INVALID_ARGUMENT`, …); разбивка по `rpc.method`.
     - Error rate = сбои сервиса; так же `otelgrpc` ставит статус `Error` серверному спану, поэтому значения совпадают
       с Service Dashboard плагина. Клиентские ошибки — отдельный столбец сводки, в error rate не входят.
     - Data link с панелей сводки → `/d/otel-service-dashboard?var-service=<сервис>` с текущим диапазоном времени.
   - **Postgres** (order/user/product/notification): p95 `db.client.operation.duration` по `pgx.operation.type`;
     утилизация пула `pgxpool.acquired_connections / pgxpool.max_connections`; время ожидания acquire
     (`pgxpool.acquire_duration`, `pgxpool.empty_acquire_wait_time`, `ns`) и число пустых acquire (`pgxpool.empty_acquire`).
   - **Kafka** (notification): `messaging.kafka.fetch_records.count` в секунду по `topic`; `messaging.kafka.read_errors.count`.
   - **Runtime** (все сервисы): `go.goroutine.count`; `go.memory.used` по `go.memory.type` (`stack`/`other`)
     и `go.memory.gc.goal`; скорость аллокаций `go.memory.allocated` (By/s).

   gRPC client (gateway→order, order→user/product/address) на дашборд не выносится: он есть в Service Dashboard (client spans).
   Метрики GC pause в каталоге нет: вместо неё — `gc.goal` и скорость аллокаций.
3. **Переходы — штатные средства OTel-режима датасорса** (`otelEnabled` для logs и traces уже включён):
   из лога с `TraceId` — в трейс (Explore), из трейса — к логам этого трейса. JSON плагина под переходы не правится.
   Из Services Overview — data link в Service Dashboard выбранного сервиса (AC2).
4. Дашборды в папке Grafana «Delivery» (`folderUid: delivery`), редактируемы в UI (`allowUiUpdates: true`), но источник
   правды — файлы в репозитории (изменения из UI экспортируются в JSON и коммитятся).
5. **Правила агрегации по temporality** (см. каталог метрик):
   - histogram и monotonic sum — **delta**: значение за окно = `sum(...)`, скорость = `sum(...) / $__interval_s`;
   - non-monotonic sum (goroutines, `go.memory.*`, `pgxpool.*_connections`) и gauge — **cumulative**/мгновенное:
     последнее значение в окне `argMax(Value, TimeUnix)` на `ServiceName` (+ атрибуты), а не сумма.
   - Перцентили — по delta-гистограммам: `sumForEach(BucketCounts)` за окно `$__interval`, индекс бакета через
     `arrayCumSum`, линейная интерполяция внутри бакета между `ExplicitBounds`.

## Tech Stack

Grafana `13.2.4`, `grafana-clickhouse-datasource` `4.23.2`, ClickHouse SQL. Без новых зависимостей и без изменений Go-кода.

## Commands

```bash
task compose-up                     # Grafana подхватывает дашборды при старте
docker compose restart grafana      # перечитать JSON после изменений (или updateIntervalSeconds в провайдере)
task watch-all                      # данные для панелей
open http://localhost:3001/dashboards/f/delivery
curl -s 'localhost:3001/api/search?folderUIDs=delivery' | jq -r '.[].uid'
docker logs grafana 2>&1 | grep -i provisioning
```

## Project Structure

```
deploy/grafana/dashboards/
  otel-logs-explorer.json        → из плагина 4.23.2 (uid otel-logs-explorer)
  otel-traces-explorer.json      → из плагина 4.23.2 (uid otel-traces-explorer)
  otel-service-dashboard.json    → из плагина 4.23.2 (uid otel-service-dashboard)
  services-overview.json         → свой дашборд (uid delivery-services-overview)
deploy/grafana/provisioning/dashboards/dashboards.yaml → folder «Delivery», folderUid delivery, путь к JSON
```

## Code Style

- JSON дашбордов форматируется (2 пробела, `jq .`), uid дашбордов и датасорса фиксированы (`delivery-services-overview`, `clickhouse`).
- SQL панелей — через макросы плагина (`$__timeFilter`, `$__timeInterval`, `$__interval_s`), без захардкоженных интервалов.
- У timeseries-панелей `interval: "10s"` (min interval) = период экспорта метрик (`defaultMetricInterval`). Без него
  `$__interval` меньше периода экспорта, и `sum(...) / $__interval_s` завышает скорость в разы (пустые бакеты чередуются с двойными).
- Переменные Services Overview: `service` (multi, из `otel_metrics_sum`, `go.goroutine.count`), фильтр `ServiceName IN (${service:singlequote})`.

Пример панели RPS:

```sql
SELECT $__timeInterval(TimeUnix) AS t,
       Attributes['http.route'] AS route,
       sum(Count) / $__interval_s AS rps
FROM otel.otel_metrics_histogram
WHERE $__timeFilter(TimeUnix)
  AND ServiceName = 'gateway'
  AND MetricName = 'http.server.request.duration'
GROUP BY t, route
ORDER BY t
```

Пример p95 по бакетам (верхняя граница бакета; в панели — с интерполяцией):

```sql
WITH b AS (
  SELECT ServiceName, any(ExplicitBounds) AS bounds, sumForEach(BucketCounts) AS counts
  FROM otel.otel_metrics_histogram
  WHERE $__timeFilter(TimeUnix) AND MetricName = 'rpc.server.call.duration'
  GROUP BY ServiceName
)
SELECT ServiceName,
       arrayFirstIndex(c -> c >= 0.95 * arraySum(counts), arrayCumSum(counts)) AS idx,
       bounds[least(idx, length(bounds))] AS p95_s
FROM b
```

## Testing Strategy

| Уровень | Что | Как |
|---|---|---|
| Provisioning | все 4 дашборда загружены в папку `delivery` без ошибок | `curl localhost:3001/api/search?folderUIDs=delivery`; логи Grafana без ошибок `provisioning` |
| Ручная | каждая панель Services Overview показывает данные под нагрузкой | `task watch-all` + `ab` на `POST /api/v1/orders` (успех и 404), просмотр за 15 мин |
| Ручная | готовые дашборды плагина работают с `clickhouse` без выбора датасорса вручную | открыть каждый, панели без ошибок |
| Ручная | переходы лог → трейс → логи | Logs Explorer / Explore: клик по `TraceId`, из трейса — «логи» |
| Ручная | data link Services Overview → Service Dashboard | клик по сервису, `var-service` подставлен |
| Сверка | p95 на панели ≈ `quantile(0.95)(Duration)` по `otel_traces` (`SpanKind='Server'`) в пределах бакета | Grafana Explore → SQL Editor |

## Boundaries

- **Always:** коммитить JSON после правок в UI; фиксировать версию плагина, из которой взяты готовые дашборды (в спеке и в `GF_PLUGINS_PREINSTALL_SYNC`).
- **Ask first:** алерты (вне скоупа итерации); materialized views/агрегирующие таблицы в ClickHouse для ускорения панелей;
  дашборды бизнес-метрик; правки JSON плагина сверх подстановки датасорса; изменение бакетов гистограмм в Go-коде.
- **Never:** держать дашборды только в volume Grafana без файлов в репозитории.

## Success Criteria

- [x] После чистого `task compose-up` в папке «Delivery» 4 дашборда без ошибок provisioning.
- [x] Все панели Services Overview показывают данные при запущенных сервисах и нагрузке.
- [x] Из лога открывается связанный трейс; из трейса — его логи (штатно, OTel-режим датасорса).
- [x] Из Services Overview есть переход в Service Dashboard выбранного сервиса.
- [x] p95 на панели совпадает со сверочным SQL в пределах точности бакетов.

## Известные ограничения

- Бакеты гистограмм крупные (`rpc.*`: `[0.001, 0.005, 0.01, 0.05, 0.1, 0.5, …]`), p95 точен до бакета. Точнее — только
  через View с `ExplicitBounds` в Go-коде (вне скоупа).
- Если p95 попадает в overflow-бакет (выше последней границы), панель показывает последнюю границу, а не реальный хвост.
- Метрики GC pause в рантайм-инструментации нет.
- При смене `OTEL_METRIC_EXPORT_INTERVAL` нужно поменять `interval` у timeseries-панелей Services Overview.
- `argMax` по cumulative-метрикам (пул, goroutines, память) берёт одну точку на группу: при нескольких репликах
  сервиса видна одна из них. Для нескольких реплик нужна группировка по `service.instance.id`.

## Решённые вопросы

1. Набор панелей подтверждён по каталогу метрик (AC2).
2. Services Overview = RED-сводка по всем сервисам + то, чего нет в плагине (pgx, Kafka, runtime); gRPC client не дублируется.
3. Переходы лог ↔ трейс — штатные средства датасорса, JSON плагина не дорабатывается.
