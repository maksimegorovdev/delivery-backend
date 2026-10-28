# Tasks: dashboards

> План: [plan.md](plan.md) · Спек: [SPEC-dashboards.md](../spec/SPEC-dashboards.md)

Grafana: http://localhost:3001/dashboards/f/delivery. SQL: `docker exec clickhouse clickhouse-client -q "<SQL>"`
или Grafana Explore → SQL Editor. Нагрузка: `task watch-all`, затем `ab` на `POST /api/v1/orders`
(успешный и с несуществующим продуктом → 404). Сценарий тот же, что в [metrics.md](../docs/observability/metrics.md).

## Task 1: Provisioning в папку «Delivery» + 3 дашборда плагина

**Description:** В `dashboards.yaml` добавить `folder: Delivery`, `folderUid: delivery`, `updateIntervalSeconds`.
Скопировать из контейнера Grafana (`/var/lib/grafana/plugins/grafana-clickhouse-datasource/dashboards/`)
`otel-logs-explorer.json`, `otel-traces-explorer.json`, `otel-service-dashboard.json`. Заменить
`${DS_GRAFANA_CLICKHOUSE_DATASOURCE}` на `clickhouse` (включая `current` переменной `datasource`), удалить `__inputs`,
отформатировать `jq .`. Удалить `.gitkeep`. Написать `deploy/grafana/dashboards/README.md`: версия плагина,
команда копирования и подстановки, порядок экспорта правок из UI.

**Acceptance criteria:**
- [x] После `docker compose restart grafana` в папке `delivery` есть `otel-logs-explorer`, `otel-traces-explorer`, `otel-service-dashboard`, в логах Grafana нет ошибок provisioning
- [x] Каждый дашборд открывается с датасорсом ClickHouse без выбора вручную. Панели показывают данные (логи, service map, RED по спанам для `order`)
- [x] Diff JSON с оригиналом плагина — только подстановка датасорса и удаление `__inputs`

**Verification:**
- [x] `curl -s 'localhost:3001/api/search?folderUIDs=delivery' | jq -r '.[].uid'` → три uid
- [x] `docker logs grafana 2>&1 | grep -iE 'provisioning.*(error|fail)'` → пусто
- [x] `diff <(jq -S 'del(.__inputs)' orig.json | sed 's/${DS_GRAFANA_CLICKHOUSE_DATASOURCE}/clickhouse/g') <(jq -S . ours.json)` → пусто
- [x] Ручная: Service Dashboard → `service=order` → ссылка «View traces for …» ведёт в Traces Explorer

**Dependencies:** None

**Files likely touched:**
- `deploy/grafana/provisioning/dashboards/dashboards.yaml`
- `deploy/grafana/dashboards/otel-logs-explorer.json`
- `deploy/grafana/dashboards/otel-traces-explorer.json`
- `deploy/grafana/dashboards/otel-service-dashboard.json`
- `deploy/grafana/dashboards/README.md`

**Estimated scope:** M

## Task 2: Переходы лог → трейс → логи штатными средствами

**Description:** Проверить, что OTel-режим датасорса даёт переходы. Из записи лога с `TraceId` (Logs Explorer или
Explore → Logs) открывается трейс. Из трейса (Explore → Traces) открываются логи этого трейса. Если чего-то не хватает,
поправить `deploy/grafana/provisioning/datasources/clickhouse.yaml` (колонки/таблицы logs ↔ traces). JSON плагина
не трогать. Если штатно не получается — остановиться и предложить пользователю варианты.

**Acceptance criteria:**
- [x] Клик по `TraceId` в записи лога `order` открывает трейс с теми же `TraceId`, спаны gateway → order → user/product видны
- [x] Из трейса открываются логи с этим `TraceId` (минимум записи `gateway` и `order`)
- [x] JSON плагина не изменён. Если менялся `clickhouse.yaml`, причина записана в README

**Verification:**
- [x] Ручная: Logs Explorer → `service=order` → раскрыть запись → ссылка на трейс
- [x] Ручная: в трейсе — кнопка или ссылка на логи. Сверка `TraceId` SQL-запросом `SELECT ServiceName, Body FROM otel.otel_logs WHERE TraceId = '<id>'`
- [x] Повторить после `docker compose restart grafana` (конфиг из provisioning, а не из UI)

**Dependencies:** Task 1

**Files likely touched:**
- `deploy/grafana/provisioning/datasources/clickhouse.yaml` (только при необходимости)
- `deploy/grafana/dashboards/README.md`

**Estimated scope:** S

## Checkpoint A: готовые дашборды и переходы
- [x] 3 дашборда в папке «Delivery», provisioning без ошибок
- [x] Переходы лог ↔ трейс работают после рестарта Grafana
- [ ] Ревью с пользователем

## Task 3: Services Overview — RED-сводка, p95 по бакетам, data link

**Description:** Создать `services-overview.json` (uid `delivery-services-overview`, title «Delivery / Services Overview»,
датасорс `clickhouse`). Переменная `service`: multi, include All, источник — `go.goroutine.count` в `otel_metrics_sum`.
Строка RED:
- gateway — RPS, error rate (5xx), p95 по `http.route` из `http.server.request.duration`;
- order/user/product — RPS, error rate (`rpc.response.status_code != 'OK'`), p95 по `rpc.method` из `rpc.server.call.duration`;
- таблица-сводка по сервисам: RPS, error %, p95.

p95 считается через `sumForEach(BucketCounts)` + `arrayCumSum` с линейной интерполяцией в бакете. Data link с сервиса
сводки → `/d/otel-service-dashboard?var-service=${__value.raw}&${__url_time_range}`.

**Acceptance criteria:**
- [x] Дашборд в папке `delivery`. Под нагрузкой RPS > 0, error rate > 0 при 404-нагрузке (gateway 5xx = 0, gRPC `NOT_FOUND` > 0)
- [x] p95 сводки для order/user/product не выше верхней границы бакета, содержащего `quantile(0.95)(Duration)` спанов `SpanKind='Server'`, за то же окно
- [ ] Клик по сервису в сводке открывает Service Dashboard с этим `service` и текущим диапазоном времени

**Verification:**
- [x] `curl -s 'localhost:3001/api/search?folderUIDs=delivery' | jq -r '.[].uid'` содержит `delivery-services-overview`
- [x] SQL: `SELECT MetricName, uniqExact(ExplicitBounds) FROM otel.otel_metrics_histogram GROUP BY 1` → у всех 1
- [x] Сверка: `SELECT ServiceName, quantile(0.95)(Duration)/1e9 FROM otel.otel_traces WHERE SpanKind='Server' AND $__timeFilter(Timestamp) GROUP BY 1` против панели
- [x] Все SQL используют `$__timeFilter`/`$__interval_s`, без захардкоженных интервалов (`grep -E "INTERVAL [0-9]" services-overview.json` → пусто)

**Dependencies:** Task 1

**Files likely touched:**
- `deploy/grafana/dashboards/services-overview.json`

**Estimated scope:** M

## Task 4: Services Overview — Postgres

**Description:** Строка Postgres с фильтром по `service` (order/user/product/notification):
- p95 `db.client.operation.duration` по `pgx.operation.type`;
- утилизация пула: `argMax` по `pgxpool.acquired_connections` / `argMax` по `pgxpool.max_connections` (gauge), по пулу;
- ожидание acquire: delta `pgxpool.acquire_duration` и `pgxpool.empty_acquire_wait_time` (ns → s), делённые на число acquire за окно, плюс `pgxpool.empty_acquire`/s.

**Acceptance criteria:**
- [ ] Под нагрузкой все панели строки показывают данные для 4 сервисов с БД. У gateway панели пустые, без ошибок
- [ ] Утилизация пула в диапазоне 0..1 (или 0..100 %). Значение совпадает с ручным SQL `argMax` за последнюю минуту
- [ ] Cumulative-метрики не суммируются по времени: график acquired не растёт монотонно при стабильной нагрузке

**Verification:**
- [ ] SQL-сверка: `SELECT ServiceName, argMax(Value, TimeUnix) FROM otel.otel_metrics_sum WHERE MetricName='pgxpool.acquired_connections' AND TimeUnix > now() - INTERVAL 1 MINUTE GROUP BY 1`
- [ ] Ручная: просмотр строки за 15 мин под нагрузкой и без неё

**Dependencies:** Task 3

**Files likely touched:**
- `deploy/grafana/dashboards/services-overview.json`

**Estimated scope:** S

## Task 5: Services Overview — Kafka и Runtime

**Description:**
- Строка Kafka (notification): `messaging.kafka.fetch_records.count` /s по `topic`; `messaging.kafka.read_errors.count` за окно.
- Строка Runtime (все сервисы из `service`): `go.goroutine.count` (argMax); `go.memory.used` по `go.memory.type` (stack/other) вместе с `go.memory.gc.goal` (argMax); скорость аллокаций `go.memory.allocated` (delta, By/s).

**Acceptance criteria:**
- [ ] После создания заказов records/s по `order.events.v1` > 0. Read errors отображаются (0 — валидное значение, не «No data»)
- [ ] Runtime-панели показывают все 5 сервисов. Значения goroutines и heap совпадают с ручным `argMax` SQL
- [ ] Success Criteria спека закрыты, в карте `dashboards` → `done`

**Verification:**
- [ ] SQL-сверка: `SELECT ServiceName, argMax(Value, TimeUnix) FROM otel.otel_metrics_sum WHERE MetricName='go.goroutine.count' GROUP BY 1`
- [ ] Ручная: чистый `task compose-up` (или удаление volume `grafana-data`) → 4 дашборда в «Delivery» без ошибок, все панели с данными под нагрузкой

**Dependencies:** Task 3

**Files likely touched:**
- `deploy/grafana/dashboards/services-overview.json`
- `spec/SPEC-dashboards.md` (Success Criteria)
- `spec/SPEC-observability-map.md` (статус)

**Estimated scope:** S

## Checkpoint B: модуль готов
- [ ] Все Success Criteria `SPEC-dashboards.md` отмечены
- [ ] Чистый старт Grafana: 4 дашборда в папке «Delivery», provisioning без ошибок
- [ ] Ревью с пользователем
