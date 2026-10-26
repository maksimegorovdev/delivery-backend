# Implementation Plan: obs-stack

> Спек: [SPEC-obs-stack.md](../SPEC-obs-stack.md) · Карта: [SPEC-observability-map.md](../SPEC-observability-map.md)
> Задачи: [todo.md](todo.md)

## Overview

Добавляем в `docker-compose` три сервиса: ClickHouse (хранилище), OTel Collector (приём OTLP, запись
в ClickHouse) и Grafana (просмотр). Конфиги кладём в `deploy/`. Go-код не меняется. Каждый шаг
проверяется без Go-сервисов: OTLP-трафик генерирует `telemetrygen`.

## Dependency Graph

```
ClickHouse (users, healthcheck)
    │
    ├── OTel Collector (otel_writer, create_schema, TTL)
    │       │
    │       └── task obs-smoke (telemetrygen → count() по otel.*)
    │
    └── Grafana (grafana_reader, datasource provisioning)
            │
            └── ручная проверка Traces/Logs на данных obs-smoke
                    │
                    └── README + статус карты
```

Ветки Collector и Grafana зависят только от ClickHouse. Grafana идёт после smoke, чтобы в ней сразу
были данные для проверки.

## Architecture Decisions

- **Схему создаёт экспортёр** (`create_schema: true`). Поэтому `otel_writer` получает в БД `otel`
  права `CREATE DATABASE`, `CREATE TABLE`, `INSERT`, `SELECT`. Своих DDL нет — это вопрос из Ask first.
- **Пользователи ClickHouse** задаются XML-файлом в `users.d` (декларативно, без SQL при старте):
  - `otel_writer` — доступ только к `otel.*`;
  - `grafana_reader` — профиль `readonly = 1`, доступ только на чтение `otel.*`;
  - `default` — с паролем, доступен только из сети compose.
  Пароли — локальные значения в compose, как у Postgres.
- **Батчинг** выполняет `sending_queue.batch` экспортёра; processor `batch` не используется (deprecated).
- **Healthcheck Collector:** образ contrib distroless (нет shell/curl), поэтому `CMD`-healthcheck внутри
  контейнера невозможен (подробнее — риск R1).
- **Grafana:** anonymous Admin; плагин ставится через `GF_PLUGINS_PREINSTALL_SYNC`; порт 3001.
- **obs-smoke** запускает `telemetrygen` через `docker run --network` в сети compose, чтобы не ставить
  бинарник на хост. Проверка строк — через `docker compose exec clickhouse clickhouse-client`.

## Task List

### Phase 1: Хранилище и приём
- [x] Task 1: ClickHouse в compose с пользователями и healthcheck
- [x] Task 2: OTel Collector пишет OTLP в ClickHouse
- [x] Task 3: Taskfile — `obs-smoke` и `obs-ch`

### Checkpoint A: путь OTLP → ClickHouse
- [x] `task obs-smoke` зелёный, TTL 72h в DDL, данные переживают рестарт

### Phase 2: Просмотр
- [x] Task 4: Grafana с provisioning датасорса ClickHouse
- [x] Task 5: README (раздел Observability) и статус в карте

### Checkpoint B: модуль завершён
- [ ] Все Success Criteria спека выполнены, ревью с пользователем

## Risks and Mitigations

| # | Риск | Влияние | Митигация |
|---|---|---|---|
| R1 | В образе Collector нет shell/curl — compose-healthcheck не выполнить | Med | Проверить при реализации, есть ли в образе бинарник для проверки. Если нет — Collector без compose-healthcheck, health проверяется в `obs-smoke` через `curl localhost:13133` с хоста. Это отклонение от AC1 согласовать с пользователем |
| R2 | `clickhouseexporter` 0.163.2 не поддерживает `sending_queue.batch` или ведёт себя иначе | Low | Collector падает при невалидном конфиге — сразу видно в Task 2. Сверить с README экспортёра этой версии |
| R3 | Collector стартует раньше ClickHouse и не может создать схему | Med | `depends_on: clickhouse: condition: service_healthy` + `retry_on_failure` |
| R4 | Плагин Grafana не скачается (нет сети) — датасорс не работает | Low | Локальная разработка подразумевает сеть; в README описать симптом |
| R5 | Конфликт портов 8123/9000/4317/4318/3001/13133 на хосте | Low | Проверить `lsof -i` до первого запуска |
| R6 | Схема `Map` vs `JSON` атрибутов: плагин определяет её автоматически, но запросы дашбордов зависят от неё | Low | Держим схему по умолчанию (`Map`), фиксируем в спеке |

## Open Questions

Нет.
