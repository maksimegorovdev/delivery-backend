# Tasks: obs-stack

> План: [plan.md](plan.md) · Спек: [SPEC-obs-stack.md](../SPEC-obs-stack.md)

## Task 1: ClickHouse в compose с пользователями и healthcheck

**Description:** Добавить сервис `clickhouse` (`clickhouse/clickhouse-server:26.8`) с named volume,
портами 8123/9000 и healthcheck `SELECT 1`. Пользователи задаются через `users.d`:
`otel_writer` (права на создание и запись в БД `otel`), `grafana_reader` (readonly) и `default` с паролем.

**Acceptance criteria:**
- [x] `docker compose ps clickhouse` → `healthy`
- [x] `otel_writer` может выполнить `CREATE DATABASE IF NOT EXISTS otel` и создать таблицу в `otel`
- [x] `grafana_reader` может выполнять `SELECT`, а любой `INSERT`/`CREATE` отклоняется с ошибкой прав

**Verification:**
- [x] `docker compose up -d clickhouse && docker compose ps clickhouse`
- [x] `docker compose exec clickhouse clickhouse-client -u otel_writer --password … -q "CREATE DATABASE IF NOT EXISTS otel"`
- [x] `docker compose exec clickhouse clickhouse-client -u grafana_reader --password … -q "CREATE TABLE otel.t (x UInt8) ENGINE=Memory"` → ошибка `READONLY`/`ACCESS_DENIED`

**Dependencies:** None

**Files likely touched:**
- `docker-compose.yml`
- `deploy/clickhouse/users.d/otel-users.xml`

**Estimated scope:** S

## Task 2: OTel Collector пишет OTLP в ClickHouse

**Description:** Добавить сервис `otel-collector` (`otel/opentelemetry-collector-contrib:0.163.2`) с
конфигом из спека: OTLP gRPC/HTTP, `memory_limiter`, `clickhouseexporter` (`otel_writer`, `create_schema`,
`ttl: 72h`, `sending_queue.batch`), `health_check` на 13133. Добавить `depends_on` от ClickHouse
с условием healthy. Разобраться с healthcheck (риск R1).

**Acceptance criteria:**
- [x] Collector стартует без ошибок конфига, `curl -f localhost:13133` → 200
- [x] После ручного запуска `telemetrygen traces|logs|metrics` в `otel` появляются `otel_traces`, `otel_logs`, `otel_metrics_*` со строками
- [x] В `SHOW CREATE TABLE otel.otel_traces` есть TTL на 72 часа

**Verification:**
- [x] `docker compose logs otel-collector` — нет `error`, есть `Everything is ready`
- [x] `docker run --rm --network <compose-net> ghcr.io/open-telemetry/opentelemetry-collector-contrib/telemetrygen:v0.163.2 traces --otlp-endpoint otel-collector:4317 --otlp-insecure --traces 5`
- [x] `clickhouse-client -q "SELECT count() FROM otel.otel_traces"` > 0

**Dependencies:** Task 1

**Files likely touched:**
- `docker-compose.yml`
- `deploy/otel-collector/config.yaml`

**Estimated scope:** S

## Task 3: Taskfile — `obs-smoke` и `obs-ch`

**Description:** `obs-smoke`: проверяет health Collector, отправляет traces/logs/metrics через `telemetrygen`
(docker, сеть compose), ждёт флаша и проверяет `count() > 0` в `otel_traces`, `otel_logs` и хотя бы
одной `otel_metrics_*`. Если какая-то таблица пустая — выходит с ненулевым кодом и называет её.
`obs-ch`: интерактивный `clickhouse-client` к БД `otel`.

**Acceptance criteria:**
- [x] `task obs-smoke` на поднятом стеке → exit 0, вывод показывает count по каждой таблице
- [x] На остановленном Collector `task obs-smoke` → ненулевой код с понятным сообщением
- [x] `task --list` показывает обе задачи с `desc`

**Verification:**
- [x] `task obs-smoke; echo $?`
- [x] `docker compose stop otel-collector && task obs-smoke; echo $?` → ≠ 0, затем `docker compose start otel-collector`

**Dependencies:** Task 2

**Files likely touched:**
- `Taskfile.yml`

**Estimated scope:** XS

## Checkpoint A: путь OTLP → ClickHouse
- [x] `task obs-smoke` зелёный
- [x] TTL 72h присутствует в DDL таблиц `otel.*`
- [x] `task compose-down && task compose-up && task obs-ch` → старые строки на месте
- [x] Существующие сервисы compose (Postgres, Redpanda, Debezium) работают как прежде
- [x] Ревью с пользователем, если R1 потребовал отклонения от спека (решение: без compose-healthcheck, спек обновлён)

## Task 4: Grafana с provisioning датасорса ClickHouse

**Description:** Добавить сервис `grafana` (`grafana/grafana:13.2.4`) на порту 3001 с named volume,
`GF_PLUGINS_PREINSTALL_SYNC=grafana-clickhouse-datasource@4.23.2`, anonymous Admin и healthcheck `/api/health`.
Provisioning: датасорс ClickHouse (native, `clickhouse:9000`, `grafana_reader`, для logs/traces
`defaultDatabase: otel`, таблицы `otel_logs`/`otel_traces`, `otelEnabled: true`), провайдер дашбордов
на пустую папку `deploy/grafana/dashboards`.

**Acceptance criteria:**
- [x] `http://localhost:3001` открывается без логина, `docker compose ps grafana` → `healthy`
- [x] Датасорс ClickHouse провиженен, `GET /api/datasources/uid/<uid>/health` → OK
- [x] Explore → Query type *Traces* показывает водопад трейса от `obs-smoke`; *Logs* показывает логи

**Verification:**
- [x] `curl -s localhost:3001/api/health`
- [x] `curl -s localhost:3001/api/datasources/uid/clickhouse/health`
- [x] Ручная проверка в Explore после `task obs-smoke` (Traces: водопад ок; логи telemetrygen без TraceId — переход trace→logs пуст, это ожидаемо)

**Dependencies:** Task 1 (лучше после Task 3 — чтобы были данные)

**Files likely touched:**
- `docker-compose.yml`
- `deploy/grafana/provisioning/datasources/clickhouse.yaml`
- `deploy/grafana/provisioning/dashboards/dashboards.yaml`
- `deploy/grafana/dashboards/.gitkeep`

**Estimated scope:** M

## Task 5: README (раздел Observability) и статус в карте

**Description:** Добавить в README раздел Observability: схема потока, адреса и порты, пользователи
ClickHouse, команды `obs-smoke`/`obs-ch`, куда смотреть в Grafana, что делать, если плагин не
установился. Добавить пункт в «Что отрабатывается». В карте отметить `obs-stack` как done,
в TODO.md — частичный прогресс по пункту Observability.

**Acceptance criteria:**
- [x] README содержит адреса и порты, логины и команды, совпадающие с compose/Taskfile
- [x] `SPEC-observability-map.md`: `obs-stack` → done

**Verification:**
- [x] Пройти README с нуля: `task compose-up` → `task obs-smoke` → открыть Grafana по инструкции

**Dependencies:** Task 4

**Files likely touched:**
- `README.md`
- `SPEC-observability-map.md`
- `TODO.md`

**Estimated scope:** XS

## Checkpoint B: модуль завершён
- [x] Все пункты Success Criteria в `SPEC-obs-stack.md` отмечены
- [ ] Ревью с пользователем → переход к спеку `telemetry-sdk`
