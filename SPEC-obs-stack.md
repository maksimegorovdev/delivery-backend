# Spec: obs-stack

> Модуль карты [SPEC-observability-map.md](SPEC-observability-map.md). Зависимостей нет.

## Objective

Поднять локальную инфраструктуру приёма, хранения и просмотра телеметрии, чтобы следующие модули
(`telemetry-sdk`, `logs`, `tracing`, `metrics`) можно было сразу проверять глазами в Grafana.

Пользователь — разработчик проекта, который запускает `task dev` и получает рабочий стек observability
вместе с остальной инфраструктурой.

Поток данных:

```
Go-сервисы (Air, host) ──OTLP gRPC :4317──▶ OTel Collector ──native :9000──▶ ClickHouse ◀── Grafana :3001
                                     (contrib, clickhouseexporter)          (db otel)     (clickhouse-datasource)
```

Модуль **не трогает Go-код** — только `docker-compose.yml`, `deploy/`, `Taskfile.yml`, документацию.

### Acceptance criteria

1. `task compose-up` поднимает `clickhouse`, `otel-collector`, `grafana` вместе с существующими сервисами;
   `clickhouse` и `grafana` в состоянии healthy. У `otel-collector` compose-healthcheck нет: в distroless-образе
   нечем его выполнить, health проверяется через `curl -f localhost:13133`.
2. Collector принимает OTLP gRPC на `localhost:4317` и OTLP HTTP на `localhost:4318`.
3. Collector пишет в ClickHouse логи, трейсы и метрики в стандартную OTel-схему экспортёра:
   БД `otel`, таблицы `otel_logs`, `otel_traces`, `otel_metrics_{gauge,sum,histogram,exponential_histogram,summary}`.
4. Данные хранятся 72 часа (TTL), чтобы локальный диск не рос бесконечно.
5. Grafana доступна на `http://localhost:3001` (порт 3000 занят redpanda-console), датасорс ClickHouse
   провиженится автоматически, с включённым OTel-режимом для логов и трейсов.
6. Grafana ходит в ClickHouse под **read-only** пользователем; Collector — под отдельным пользователем с правом записи в `otel`.
7. Данные ClickHouse и Grafana переживают `compose-down`/`compose-up` (named volumes).

## Tech Stack

| Компонент | Образ | Версия |
|---|---|---|
| ClickHouse | `clickhouse/clickhouse-server` | `26.8` (LTS) |
| OTel Collector | `otel/opentelemetry-collector-contrib` | `0.163.2` |
| Grafana | `grafana/grafana` | `13.2.4` |
| ClickHouse datasource | плагин `grafana-clickhouse-datasource` | `4.23.2` |
| Генератор тестовой телеметрии (ручной запуск) | `ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen` | `v0.163.0` (тега `v0.163.2` нет) |

Версии фиксируются точными тегами, `latest` не используется.

## Commands

```bash
task compose-up        # docker compose up -d --build (включая obs-стек)
task compose-down      # остановить инфраструктуру

docker compose ps clickhouse otel-collector grafana   # статус health
docker compose logs -f otel-collector                 # отладка пайплайна
```

## Project Structure

```
docker-compose.yml                         → + сервисы clickhouse, otel-collector, grafana; + volumes
deploy/
  clickhouse/
    users.d/otel-users.xml                 → пользователи otel_writer (запись в otel) и grafana_reader (readonly)
  otel-collector/
    config.yaml                            → receivers otlp; processors memory_limiter; exporter clickhouse (батчинг в sending_queue)
  grafana/
    provisioning/
      datasources/clickhouse.yaml          → датасорс ClickHouse (native, OTel-режим logs/traces)
      dashboards/dashboards.yaml           → провайдер дашбордов (папка пустая до модуля dashboards)
    dashboards/                            → JSON дашбордов (заполняется в модуле dashboards)
README.md                                  → + раздел Observability (адреса, логины)
```

Порты на хосте:

| Сервис | Порт | Назначение |
|---|---|---|
| ClickHouse | 8123 | HTTP-интерфейс |
| ClickHouse | 9000 | native-протокол |
| Collector | 4317 / 4318 | OTLP gRPC / HTTP |
| Collector | 13133 | health_check extension |
| Grafana | 3001 | UI |

## Code Style

Конфиги в стиле существующего `docker-compose.yml`: `container_name` = имя сервиса, явные тэги образов,
named volumes, без лишних комментариев. Конфиг Collector — минимальный и читаемый:

```yaml
receivers:
  otlp:
    protocols:
      grpc: { endpoint: 0.0.0.0:4317 }
      http: { endpoint: 0.0.0.0:4318 }

processors:
  memory_limiter: { check_interval: 1s, limit_percentage: 80, spike_limit_percentage: 20 }

exporters:
  clickhouse:
    endpoint: tcp://clickhouse:9000?dial_timeout=10s
    database: otel
    username: ${env:CLICKHOUSE_USER}
    password: ${env:CLICKHOUSE_PASSWORD}
    create_schema: true
    ttl: 72h
    sending_queue: { batch: {} }
    retry_on_failure: { enabled: true }

extensions:
  health_check: { endpoint: 0.0.0.0:13133 }

service:
  extensions: [health_check]
  pipelines:
    traces:  { receivers: [otlp], processors: [memory_limiter], exporters: [clickhouse] }
    metrics: { receivers: [otlp], processors: [memory_limiter], exporters: [clickhouse] }
    logs:    { receivers: [otlp], processors: [memory_limiter], exporters: [clickhouse] }
```

Соглашения:
- Креды передаются через `environment` в compose, в конфиги — через `${env:...}`; в файлах конфигов секретов нет.
- Схему таблиц создаёт экспортёр (`create_schema: true`) — для локальной среды этого достаточно;
  атрибуты хранятся в `Map(String, String)` (схема по умолчанию, поддерживается плагином Grafana).

## Testing Strategy

Go-тестов в модуле нет — это инфраструктура. Проверка:

| Уровень | Что проверяет | Как |
|---|---|---|
| Health | контейнеры поднялись | healthcheck в compose: `clickhouse` — `SELECT 1`, `grafana` — `/api/health`; `otel-collector` — `curl -f localhost:13133` |
| E2E | путь OTLP → Collector → ClickHouse | `docker run --rm --network container:otel-collector <telemetrygen> traces\|logs\|metrics --otlp-endpoint localhost:4317 --otlp-insecure` → строки в `otel_traces`, `otel_logs`, `otel_metrics_*` (Grafana → SQL Editor) |
| Ручная | датасорс и OTel-режим в Grafana | Explore → ClickHouse → Query type *Traces* / *Logs* показывает данные от `telemetrygen`; датасорс «Save & test» зелёный |
| Права | Grafana не может писать | `INSERT` под `grafana_reader` возвращает ошибку доступа |

## Boundaries

- **Always:**
  - Фиксировать точные версии образов и плагина.
  - Держать конфиги в `deploy/<component>/`, по образцу `deploy/debezium`.
  - После изменений конфигов стека проверять в Grafana Explore, что телеметрия доходит до ClickHouse.
  - Обновлять README при изменении портов и логинов.
- **Ask first:**
  - Смена портов существующих сервисов compose.
  - Переход на ручное управление схемой (`create_schema: false`, свои DDL) или JSON-тип атрибутов.
  - Добавление компонентов вне карты (Tempo, Loki, Prometheus, ClickHouse Keeper/кластер).
  - Изменение TTL.
- **Never:**
  - Изменять Go-код сервисов и `platform` в рамках этого модуля.
  - Давать Grafana пользователя с правами записи или `default` без пароля.
  - Использовать тэги `latest`.

## Success Criteria

- [x] `docker compose ps` показывает `clickhouse` и `grafana` в состоянии `healthy`, `otel-collector` — `Up`, `:13133` отвечает 200.
- [x] Трейсы, логи и метрики от `telemetrygen` появляются в `otel_traces`, `otel_logs`, `otel_metrics_*`.
- [x] `SHOW CREATE TABLE otel.otel_traces` содержит `TTL` на 72 часа.
- [x] `http://localhost:3001` открывается, датасорс ClickHouse провиженен и проходит «Save & test».
- [x] В Explore с Query type *Traces* открывается трейс от `telemetrygen` в виде водопада спанов.
- [x] `INSERT` под `grafana_reader` отклоняется.
- [x] После `task compose-down && task compose-up` данные в `otel_*` сохраняются.
- [x] README содержит раздел Observability с адресами и логинами.

## Decisions

1. **Логин Grafana:** anonymous-доступ с ролью Admin (`GF_AUTH_ANONYMOUS_ENABLED=true`, `GF_AUTH_ANONYMOUS_ORG_ROLE=Admin`) — среда только локальная.
2. **Батчинг:** processor `batch` deprecated в пользу батчинга экспортёра — используется `sending_queue.batch` в `clickhouseexporter`, processor `batch` не подключается.
3. **Установка плагина:** `GF_PLUGINS_PREINSTALL_SYNC=grafana-clickhouse-datasource@4.23.2` — плагин ставится до старта Grafana.

## Open Questions

Нет.
