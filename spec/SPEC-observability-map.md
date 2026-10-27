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

Build order: `obs-stack` → `telemetry-sdk` → `logs`, `tracing`, `metrics` (параллельно) → `async-propagation` → `dashboards`

## Спеки модулей

| Module id | Спек | Статус |
|---|---|---|
| `obs-stack` | [SPEC-obs-stack.md](SPEC-obs-stack.md) | done |
| `telemetry-sdk` | [SPEC-telemetry-sdk.md](SPEC-telemetry-sdk.md) | approved |
| `logs` | [SPEC-logs.md](SPEC-logs.md) | approved |
| `tracing` | [SPEC-tracing.md](SPEC-tracing.md) | approved |
| `metrics` | [SPEC-metrics.md](SPEC-metrics.md) | approved |
| `async-propagation` | [SPEC-async-propagation.md](SPEC-async-propagation.md) | approved |
| `dashboards` | [SPEC-dashboards.md](SPEC-dashboards.md) | draft (предварительный) |

## Принятые решения

- Трейсы хранятся и просматриваются в ClickHouse (Query type **Traces** плагина `grafana-clickhouse-datasource`), без Tempo.
- Сервисы (запускаются через Air вне Docker) шлют OTLP gRPC на `localhost:4317` в Collector; логи дублируются в stdout.
- Асинхронный трейс пробрасывается через outbox (не начинается заново в notification).
- Метрики первой итерации — технические + RED; бизнес-метрики отложены.
- Grafana: датасорс и дашборды через provisioning, алерты — следующий этап.
