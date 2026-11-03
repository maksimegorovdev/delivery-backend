# Capability Map: Observability

Базовый observability (логи, метрики, трейсы) для всех сервисов на стеке
OpenTelemetry → OTel Collector → ClickHouse → Grafana.

Единственное хранилище телеметрии — ClickHouse. Tempo, Loki, Prometheus не используются.

| Module id | Ответственность | Зависит от |
|---|---|---|
| `obs-stack` | Инфраструктура в docker-compose: ClickHouse, OTel Collector (contrib, OTLP, `clickhouseexporter`), Grafana с ClickHouse-датасорсом (provisioning). Конфиги в `deploy/` | — |
| `telemetry-sdk` | Пакет `platform/telemetry`: Resource, Tracer/Meter/LoggerProvider, OTLP-экспортёры, W3C-пропагатор, shutdown через `closer`. Env-конфиг `OTEL_*` во всех сервисах | `obs-stack` (только для e2e-проверки) |
| `logs` | `platform/logger`: fan-out slog → stdout + `otelslog`, `trace_id`/`span_id` в записях, сохранение `request_id` | `telemetry-sdk` |
| `tracing` | Автоинструментация sync-пути: `otelhttp` (gateway), `otelgrpc` stats handler (сервер и клиент), `otelpgx`, `kotel` (consumer) | `telemetry-sdk` |
| `metrics` | RED-метрики HTTP/gRPC/pgx/Kafka из автоинструментации, runtime-метрики Go | `telemetry-sdk` |
| `async-propagation` | `traceparent` через outbox → Debezium (headers) → Kafka → notification: миграция outbox, коннектор, extract в консьюмере | `tracing` |
| `dashboards` | Grafana dashboards as code: готовые OTel-дашборды плагина + RED-обзор сервисов, переходы логи ↔ трейсы | `logs`, `tracing`, `metrics` |
| `profiling` | `platform/debugserver` (pprof по `PPROF_ADDR`), автоснимки профилей в задачах `load-stress`/`load-soak`, continuous profiling (Grafana Alloy `pyroscope.scrape` → Grafana Pyroscope; отступление от «только ClickHouse» зафиксировано в «Принятых решениях») | `dashboards`, [`testing/load`](../testing/map.md) |

Build order: `obs-stack` → `telemetry-sdk` → `logs`, `tracing`, `metrics` (параллельно) → `async-propagation` → `dashboards` → `profiling` (после `testing/load`)

## Спеки модулей

| Module id | Спек | Статус |
|---|---|---|
| `obs-stack` | [spec](obs-stack/spec.md) | done |
| `telemetry-sdk` | [spec](telemetry-sdk/spec.md) | done |
| `logs` | [spec](logs/spec.md) | done |
| `tracing` | [spec](tracing/spec.md) | done |
| `metrics` | [spec](metrics/spec.md) | done |
| `async-propagation` | [spec](async-propagation/spec.md) | done |
| `dashboards` | [spec](dashboards/spec.md) | done |
| `profiling` | [spec](../../changes/observability/profiling/spec.md) | approved |

## Принятые решения

- Трейсы хранятся и просматриваются в ClickHouse (Query type **Traces** плагина `grafana-clickhouse-datasource`), без Tempo.
- Сервисы (запускаются через Air вне Docker) шлют OTLP gRPC на `localhost:4317` в Collector; логи дублируются в stdout.
- Асинхронный трейс пробрасывается через outbox (не начинается заново в notification).
- Метрики первой итерации — технические + RED; бизнес-метрики отложены.
- Grafana: датасорс и дашборды через provisioning, алерты — следующий этап.
- Профили хранятся в Grafana Pyroscope, а не в ClickHouse: в ClickHouse-экспортёре OTel Collector нет сигнала profiles,
  а сам сигнал profiles в OTel ещё не стабилен (Alpha). Логи, метрики и трейсы по-прежнему только в ClickHouse.
- Профили собирает Grafana Alloy (`pyroscope.scrape` → `pyroscope.write`), а не OTel Collector: `pprofreceiver`
  сливает mutex и block в один тип `contentions`, теряет resource-атрибуты с точками и плодит label `bytes` на heap
  (spike 2026-11-03). Конфиг Collector профилями не затрагивается.
