# Spec: dashboards

> Модуль карты [SPEC-observability-map.md](SPEC-observability-map.md). Зависит от `logs`, `tracing`, `metrics`.
>
> ⚠️ **Предварительный спек.** SQL-запросы панелей зависят от реальных имён метрик и атрибутов.
> Перед планом спек уточняется по `docs/observability/metrics.md` (модуль `metrics`) и данным в ClickHouse.

## Objective

Дашборды Grafana as code (provisioning из `deploy/grafana/dashboards`), чтобы после `task dev` сразу видеть
состояние сервисов и переходить между метриками, логами и трейсами без ручной настройки.

### Acceptance criteria

1. **Готовые OTel-дашборды плагина** (`grafana-clickhouse-datasource` 4.23.2) провиженятся из файлов, а не импортируются кнопкой:
   - OpenTelemetry Logs Explorer;
   - OpenTelemetry Traces Explorer (поиск трейсов, service map);
   - OpenTelemetry Service Dashboard (RED по спанам, ошибки, медленные операции, логи сервиса).
   JSON берётся из исходников плагина зафиксированной версии, uid датасорса подставлен.
2. **Свой дашборд «Delivery / Services Overview»** на метриках (`otel_metrics_*`), переменные `service`, `$__interval`:
   - HTTP gateway: RPS, error rate (5xx), p50/p95/p99 по `http.route`;
   - gRPC server: RPS, error rate (код ≠ OK), p95 по `rpc.method` для order/user/product;
   - gRPC client: p95 вызовов gateway→order, order→user/product;
   - Postgres: p95 запросов, утилизация пула (acquired / max), ожидание acquire;
   - Kafka: consumed records/s по топику в notification;
   - Runtime: goroutines, heap, GC pause по сервису.
3. **Переходы:** из лога с `TraceId` — в трейс (OTel-режим датасорса); из трейса — к логам этого трейса;
   из панелей Services Overview — data link в Service Dashboard для выбранного сервиса.
4. Дашборды в папке Grafana «Delivery», редактируемы в UI, но источник правды — файлы в репозитории
   (изменения из UI экспортируются в JSON и коммитятся).
5. Перцентили считаются по delta-гистограммам (`otel_metrics_histogram`: `BucketCounts`, `ExplicitBounds`) за окно `$__interval`.

## Tech Stack

Grafana `13.2.4`, `grafana-clickhouse-datasource` `4.23.2`, ClickHouse SQL. Без новых зависимостей.

## Commands

```bash
task compose-up                     # Grafana подхватывает дашборды при старте
docker compose restart grafana      # перечитать JSON после изменений (или updateIntervalSeconds в провайдере)
task watch-all                      # данные для панелей
open http://localhost:3001/dashboards/f/delivery
```

## Project Structure

```
deploy/grafana/dashboards/
  otel-logs-explorer.json        → из плагина 4.23.2
  otel-traces-explorer.json      → из плагина 4.23.2
  otel-service.json              → из плагина 4.23.2
  services-overview.json         → свой RED-дашборд
deploy/grafana/provisioning/dashboards/dashboards.yaml → папка «Delivery», путь к JSON
```

## Code Style

- JSON дашбордов форматируется (2 пробела), uid дашбордов и датасорса фиксированы (`delivery-services-overview`, `clickhouse`).
- SQL панелей — через макросы плагина (`$__timeFilter`, `$__timeInterval`), без захардкоженных интервалов.

Пример панели RPS (уточняется по фактическим именам метрик):

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

## Testing Strategy

| Уровень | Что | Как |
|---|---|---|
| Provisioning | все 4 дашборда загружены без ошибок | `curl localhost:3001/api/search?folderUIDs=delivery`; логи Grafana без `provisioning` ошибок |
| Ручная | каждая панель показывает данные под нагрузкой | `task watch-all` + генерация запросов, просмотр за 15 мин |
| Ручная | переходы лог → трейс → логи работают | клик по TraceId в Logs Explorer |
| Сверка | p95 на панели ≈ p95 из прямого SQL по `otel_traces` (`quantile(0.95)(Duration)`) | Grafana Explore → SQL Editor |

## Boundaries

- **Always:** коммитить JSON после правок в UI; фиксировать версию плагина, из которой взяты готовые дашборды.
- **Ask first:** алерты (вне скоупа итерации); materialized views/агрегирующие таблицы в ClickHouse для ускорения панелей; дашборды бизнес-метрик.
- **Never:** держать дашборды только в volume Grafana без файлов в репозитории.

## Success Criteria

- [ ] После чистого `task compose-up` в папке «Delivery» 4 дашборда без ошибок provisioning.
- [ ] Все панели Services Overview показывают данные при запущенных сервисах и нагрузке.
- [ ] Из лога в Logs Explorer открывается связанный трейс; из трейса — его логи.
- [ ] Из Services Overview есть переход в Service Dashboard выбранного сервиса.
- [ ] p95 на панели совпадает со сверочным SQL в пределах точности бакетов.

## Open Questions

1. Подтвердить набор панелей Services Overview после модуля `metrics` (по фактическому каталогу метрик).
2. Если готовые дашборды плагина уже покрывают RED по спанам достаточно — возможно, Services Overview стоит ограничить
   тем, чего в них нет (pgx pool, runtime, Kafka). Решим на этапе уточнения.
