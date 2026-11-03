# Лекция 2. Observability и Grafana в delivery-backend

> Цель: понять, откуда берутся логи, метрики, трейсы и профили, где они хранятся, и **научиться читать
> дашборды Grafana** и расследовать проблемы. Предыдущая лекция — [01-testing.md](01-testing.md).
> Спеки: [`spec/specs/observability/map.md`](../../spec/specs/observability/map.md) и модули рядом.

---

## 0. Что такое observability

**Мониторинг** отвечает на заранее известные вопросы («жив ли сервис?»). **Observability** — свойство
системы, при котором по её внешним сигналам можно ответить на **новый** вопрос, не добавляя кода:
«почему вчера в 14:05 у 3% заказов была задержка 2 секунды?».

Сигналы, которые есть в проекте:

| Сигнал | Что это | На какой вопрос отвечает | Пример |
|---|---|---|---|
| **Метрики** | Числа, агрегированные во времени | **Что** происходит и **сколько**? | RPS, p95, % ошибок, горутины |
| **Трейсы** | Путь одного запроса через все сервисы | **Где** тратится время? | 76 мс из 108 — ожидание пула БД |
| **Логи** | Отдельные события с текстом и атрибутами | **Почему** именно так? | `product not found: id=...` |
| **Профили** | Распределение CPU/памяти по функциям | **Какой код** тратит ресурс? | 40% CPU — JSON-сериализация |

Типичный путь расследования: **метрика** показала проблему → **трейс** показал, где она → **лог** или
**профиль** показал причину. Grafana здесь — одно окно, где всё это связано переходами.

---

## 1. Архитектура: как данные попадают в Grafana

```
 ┌───────── Go-сервисы (через Air, вне Docker) ──────────┐
 │ gateway  order  user  product  notification           │
 │   │  platform/telemetry: OTel SDK (traces, metrics)   │
 │   │  platform/logger:    slog → stdout + otelslog     │
 │   │  platform/debugserver: /debug/pprof на 6060-6064  │
 └───┼───────────────────────────────────────┼───────────┘
     │ OTLP gRPC :4317                        │ HTTP pprof (scrape)
     ▼                                        ▼
 ┌──────────────────┐                  ┌──────────────┐
 │  OTel Collector  │                  │ Grafana Alloy│
 │ memory_limiter → │                  └──────┬───────┘
 │ clickhouse exp.  │                         ▼
 └───────┬──────────┘                  ┌──────────────┐
         ▼                             │  Pyroscope   │ :4040
 ┌──────────────────────────────┐      └──────┬───────┘
 │ ClickHouse  (БД otel, TTL 72h)│             │
 │  otel_traces                 │             │
 │  otel_logs                   │             │
 │  otel_metrics_histogram/sum/…│             │
 └───────┬──────────────────────┘             │
         └──────────────┬─────────────────────┘
                        ▼
                ┌───────────────┐
                │   Grafana     │  http://localhost:3001
                └───────────────┘
```

Ключевые решения проекта:

- **Одно хранилище — ClickHouse** для логов, метрик и трейсов (без Prometheus, Loki, Tempo). Плюс: всё
  запрашивается SQL'ем, можно делать JOIN между сигналами. Минус: дашборды пишутся на SQL, а не на PromQL.
- **Профили — отдельно в Pyroscope**: в ClickHouse-экспортёре нет сигнала profiles.
- **Данные живут 72 часа** (`ttl: 72h` в `deploy/otel-collector/config.yaml`).
- Источник всей телеметрии — **автоинструментация** OpenTelemetry: `otelhttp` (gateway), `otelgrpc`
  (клиент и сервер), `otelpgx` (Postgres), `kotel` (Kafka), runtime-метрики Go. Бизнес-метрик пока нет.

### Как запустить

```bash
task compose-up                  # инфраструктура + ClickHouse + Collector + Grafana + Pyroscope + Alloy
# в services/<svc>/.env:  OTEL_ENABLED=true   (в .env.example по умолчанию false!)
task migrate-up
task watch-all                   # сервисы с hot-reload
open http://localhost:3001       # Grafana, вход не нужен (anonymous Admin)
```

Чтобы на дашбордах было что смотреть, нужен трафик: создайте несколько заказов через Swagger gateway
(`http://localhost:8080/swagger/`) или используйте нагрузочный стек (раздел 9).

Порты для справки: Grafana **3001**, ClickHouse HTTP **8123**, Collector OTLP **4317/4318**,
Pyroscope **4040**, Alloy **12345**, Redpanda Console **3000**.

---

## 2. Grafana: базовая грамотность

### 2.1. Основные понятия

- **Data source** — откуда Grafana берёт данные. У нас два: `clickhouse` (uid `clickhouse`) и Pyroscope.
  Настроены через provisioning: `deploy/grafana/provisioning/datasources/`.
- **Dashboard** — набор панелей. Наши лежат в папке **Delivery** и в репозитории в
  `deploy/grafana/dashboards/*.json` (dashboards as code). Менять можно в UI, но изменения нужно
  экспортировать в JSON и закоммитить — иначе они потеряются при пересоздании контейнера.
- **Panel** — один график/таблица/число. За каждой панелью — **запрос** (у нас SQL к ClickHouse).
- **Row** — сворачиваемая группа панелей.
- **Variables** — выпадающие списки вверху дашборда (`service`, `operation`, …). Подставляются в SQL.
- **Explore** — режим «свободного поиска» без дашборда: пишете запрос и смотрите результат.

### 2.2. Интерфейс дашборда, который нужно знать

1. **Time range** (вверху справа): `Last 15 minutes`, `Last 1 hour`… Всё на дашборде считается за этот диапазон.
   **Выделение мышью** на графике — зум в этот интервал. Кнопка «лупа с минусом» — отдалить.
2. **Refresh**: стрелка и выпадашка `5s/10s/30s` — автообновление. При нагрузочном тесте ставьте `10s`.
3. **Переменные**: выберите `service = order` — все панели перестроятся только под него.
4. **Легенда** под графиком: клик по серии — показать только её; `Ctrl/Cmd+клик` — добавить/убрать.
5. **Tooltip**: наведите на график — значения всех серий в этой точке.
6. **Меню панели** (три точки или клавиша `e` при наведении):
   - **View** (`v`) — панель на весь экран;
   - **Edit** — открыть запрос и настройки визуализации (главный способ понять, что панель показывает);
   - **Inspect → Data** — сырые числа; **Inspect → Query** — итоговый SQL с подставленными переменными;
   - **Explore** — открыть запрос панели в Explore.
7. **Data links** — кликабельные значения. В «Services Overview» клик по сервису ведёт в его Service Dashboard.

> Совет: открывая незнакомую панель, всегда загляните в **Edit** и прочитайте SQL. Через неделю вы будете
> понимать дашборды лучше, чем их описание.

---

## 3. Метрики: что нужно понимать, чтобы читать графики

### 3.1. RED и USE

Два метода, на которых построены дашборды:

- **RED** — для всего, что обслуживает запросы (HTTP, gRPC):
  - **R**ate — сколько запросов в секунду;
  - **E**rrors — какая доля завершилась ошибкой;
  - **D**uration — сколько длятся (перцентили).
- **USE** — для ресурсов (пул соединений, CPU, память):
  - **U**tilization — насколько занят (acquired / max);
  - **S**aturation — есть ли очередь (ожидание acquire, empty acquire);
  - **E**rrors — ошибки ресурса (canceled acquires).

Правило: **RED показывает симптом, USE — причину.** p95 вырос (RED) → пул pgx занят на 100% и растёт
ожидание (USE) → вот и причина.

### 3.2. Перцентили и гистограммы

Длительность запросов хранится как **гистограмма**: не каждое значение, а количество запросов в
**бакетах** (корзинах) с границами `ExplicitBounds`. Для gRPC: `[0.001, 0.005, 0.01, 0.05, 0.1, 0.5, …]` секунд.

```
бакет:        ≤1ms  ≤5ms  ≤10ms  ≤50ms  ≤100ms  ...
запросов:      120   800    60     15      5
накопительно:  120   920   980    995   1000
```

p95 = значение, ниже которого 95% запросов = 950-й запрос → попадает в бакет `5–10 мс`. Внутри бакета
значение интерполируется линейно. Отсюда важные следствия:

- **p95 точен с точностью до бакета.** Между 10 и 50 мс график не различит 12 и 45 мс так хорошо, как хотелось бы.
- Если хвост выше последней границы — панель покажет последнюю границу, а не реальное значение.
- Для точной цифры сверяйтесь с трейсами: `quantile(0.95)(Duration)` по `otel_traces` (раздел 8).

### 3.3. Delta vs cumulative (почему SQL выглядит так, как выглядит)

Сервисы экспортируют метрики раз в 10 с. Есть два вида:

- **delta** (счётчики и гистограммы) — каждая точка = «сколько случилось за последние 10 с».
  Скорость = `sum(Count) / $__interval_s`.
- **cumulative / мгновенные** (горутины, память, `pgxpool.*_connections`) — каждая точка = текущее значение.
  Суммировать нельзя, берётся последнее: `argMax(Value, TimeUnix)`.

Поэтому у timeseries-панелей стоит `Min interval = 10s` (= период экспорта). Если меньше — появятся
«пустые» и «двойные» точки и скорость будет скакать.

### 3.4. Каталог метрик

Полный список — [`metrics-catalog.md`](../../spec/specs/observability/metrics/metrics-catalog.md). Главные:

| Метрика | Что значит |
|---|---|
| `http.server.request.duration` | Длительность HTTP в gateway (по `http.route`, `http.response.status_code`) |
| `rpc.server.call.duration` | Длительность входящих gRPC в order/user/product (по `rpc.method`, `rpc.response.status_code`) |
| `rpc.client.call.duration` | Исходящие gRPC (gateway → order, order → user/product) |
| `db.client.operation.duration` | Операции с Postgres (`query`, `acquire`, `connect`, …) |
| `pgxpool.acquired_connections` / `max_connections` | Занятые / максимум соединений пула |
| `pgxpool.empty_acquire`, `empty_acquire_wait_time` | Сколько раз пришлось **ждать** соединение и сколько суммарно ждали |
| `messaging.kafka.fetch_records.count` | Сообщений, вычитанных notification |
| `go.goroutine.count`, `go.memory.used`, `go.memory.allocated` | Runtime Go |

---

## 4. Дашборд «Delivery / Services Overview» — главный экран

`deploy/grafana/dashboards/services-overview.json`. Открывайте его первым: состояние **всех** сервисов сразу.
Переменная `service` — мультивыбор.

### Row «Сводка» — таблица RED по сервисам

| Колонка | Как считается | Как читать |
|---|---|---|
| `rps` | все запросы / длительность диапазона | Сравнивайте сервисы: order получает на каждый заказ 1 вызов, user — 2 (пользователь + адрес) |
| `error_rate` | **серверные** ошибки: HTTP 5xx; gRPC `UNKNOWN`, `INTERNAL`, `UNAVAILABLE`, `DEADLINE_EXCEEDED`, `UNIMPLEMENTED`, `DATA_LOSS` | **Должно быть 0.** Любое ненулевое значение — сбой нашего кода или инфраструктуры |
| `client_error_rate` | HTTP 4xx; gRPC `NOT_FOUND`, `INVALID_ARGUMENT`, … | Ошибки клиента. Не повод паниковать, но всплеск — повод проверить, не сломали ли API |
| `p95` | по бакетам гистограммы с интерполяцией | Сравнивайте с обычным уровнем (несколько мс) |

Клик по имени сервиса → **OpenTelemetry Service Dashboard** этого сервиса.

### Row «Gateway: HTTP server» и «gRPC server»

RPS, error rate и p95 **во времени** с разбивкой по маршруту/методу. Здесь видно, **когда** началось.
Что искать:
- RPS упал, а клиенты не уменьшали нагрузку → запросы не доходят или висят;
- p95 одного метода вырос, остальных — нет → проблема в зависимостях этого метода;
- p95 всех методов вырос одновременно → общий ресурс (БД, CPU, GC).

### Row «Postgres (pgxpool)» — USE для пула соединений

| Панель | Норма | Тревога |
|---|---|---|
| Postgres p95 по операциям | `query` — единицы мс | Рост `acquire` = ожидание соединения, а не медленный SQL |
| Утилизация пула | далеко от 1.0 | Полка на 1.0 (100%) — все соединения заняты |
| Ожидание acquire | ≈ 0 | Любой устойчивый рост — запросы стоят в очереди |
| Empty acquire /s | ≈ 0 | Частые «пустые» acquire = пул мал для нагрузки |

Именно эта группа объяснила предел в 375–400 RPS при нагрузочном тесте (см. лекцию 1, раздел 6.4).

### Row «Kafka (notification)»

- **fetch records /s** — темп чтения. Должен совпадать с темпом создания заказов. Если ниже — notification
  отстаёт (лаг растёт).
- **read errors за окно** — должно быть 0.

### Row «Runtime (Go)»

| Панель | Как читать |
|---|---|
| Goroutines | Под нагрузкой растёт, после неё **возвращается**. Если растёт «лесенкой» и не падает — утечка |
| Память: used и GC goal | `used` — пила: растёт, GC собирает. Растущее **дно** пилы — утечка памяти |
| Скорость аллокаций | Пропорциональна RPS. Резкий рост на том же RPS — новый «жадный» код |

---

## 5. Готовые OTel-дашборды плагина ClickHouse

Три дашборда взяты из плагина `grafana-clickhouse-datasource` 4.23.2 и работают по **спанам и логам**,
а не по метрикам.

### 5.1. OpenTelemetry Service Dashboard — «рентген» одного сервиса

Выберите `service` вверху. Строки (rows) по **виду спана** (`SpanKind`):

| Row | Какие спаны | Пример для order |
|---|---|---|
| Server | входящие запросы | `order.v1.OrderService/CreateOrder` |
| Client | исходящие вызовы | gRPC в user/product, SQL-запросы `otelpgx` |
| Internal | внутренние операции | |
| Async (Producer + Consumer) | работа с Kafka | consumer в notification |
| Errors & recent logs | топ ошибок и свежие логи сервиса | |

В каждой строке: Request Rate, Error Rate, Duration Percentiles (p50/p90/p95/p99) и таблица
**Slowest Operations** — с неё удобно начинать: она сразу говорит, какая операция тормозит.

Разница со Services Overview: там — метрики (точные счётчики, дешёвые), здесь — спаны (можно провалиться
в конкретный медленный запрос).

### 5.2. OpenTelemetry Traces Explorer — поиск трейсов

- **Service Map** (node graph) — кто кого вызывает. Должно получиться:
  `gateway → order → user, product`, `order → Postgres`, `notification → Postgres`.
  Толщина/цифры — объём вызовов, цвет — доля ошибок.
- **Trace Search** — фильтры `service`, `operation`, `status`, `min_duration`, `search`.
  Типичный запрос: `service=gateway`, `min_duration=50ms` — «покажи медленные запросы».
- Клик по `TraceId` открывает **трейс** (см. раздел 6).
- **Duration Distribution** (heatmap) — по X время, по Y длительность, цвет — число спанов. Горизонтальная
  полоса внизу — нормальные запросы; отдельные точки высоко — выбросы; «облако», ползущее вверх — деградация.

### 5.3. OpenTelemetry Logs Explorer — логи

- **Log Volume by Level** — объём логов по уровням во времени. Всплеск `ERROR` = событие для расследования.
  В проекте ошибки клиента (404, 400) и отменённые запросы пишутся уровнем `WARN`, а сбои сервера
  (например, недоступная зависимость) — `ERROR`. Текст сообщения общий (`grpc request failed`),
  причина — в атрибутах `error.cause`, `error.code`.
- **Services**, **Level Distribution**, **Top Error Messages** — какие ошибки самые частые.
- **Log Samples** — сами строки. Фильтры: `service`, `level_filter`, `search` (подстрока).
- Раскройте строку лога: у записи, сделанной в контексте запроса, есть `TraceId` → ссылка **в трейс**.
  Есть и `request_id` (в `LogAttributes`) — для поиска всех логов одного запроса.

### 5.4. Delivery / Load

`deploy/grafana/dashboards/load.json` — для нагрузочного стека (Grafana на `http://localhost:38300`).
Совмещает на одном экране **генератор** и **систему**:

- **k6**: RPS целевой и достигнутый (расхождение = система не тянет), latency k6, ошибки,
  `dropped_iterations` и VU, таблица по ступеням;
- **RED сервисов**: RPS, error rate, p95, p99;
- **Runtime** и **пул pgx**: утилизация, empty acquire, время ожидания, canceled acquires.

Как читать stress-прогон: смотрите по ступеням, где первым «ломается» что-то. Если p95 вырос, а CPU нет,
но выросло ожидание пула — узкое место в пуле, а не в коде.

---

## 6. Трейсы: как читать waterfall

### 6.1. Термины

- **Trace** — дерево работы по одному запросу. Идентификатор `TraceId` (32 hex-символа).
- **Span** — одна операция: имя, начало, длительность, статус, атрибуты. `SpanId`, `ParentSpanId`.
- **SpanKind**: `Server` (принял запрос), `Client` (сделал запрос), `Producer`/`Consumer` (Kafka), `Internal`.
- **Status**: `Error` у спана — операция завершилась сбоем.
- **Links** — связь с другим трейсом без отношения «родитель-потомок».
- **Context propagation** — передача `TraceId` между сервисами в заголовке `traceparent` (W3C):
  `00-<trace-id>-<span-id>-01`.

### 6.2. Как выглядит трейс создания заказа

Реальный трейс из dev-окружения (старт от начала трейса и длительность, мс):

```
 start  dur   сервис        kind      спан
   0.0  10.7  gateway       Server    POST /api/v1/orders
   0.2  10.2  gateway       Client      order.v1.OrderService/CreateOrder
   0.7   9.5  order         Server        order.v1.OrderService/CreateOrder
   0.8   2.5  order         Client          user.v1.UserService/GetUser
   1.2   2.0  user          Server            user.v1.UserService/GetUser
   1.4   0.03 user          Client              pool.acquire
   1.4   1.5  user          Client              SELECT
   3.4   1.1  order         Client          address.v1.AddressService/GetAddress
   4.6   1.7  order         Client          product.v1.ProductService/GetProducts
   6.3   0.01 order         Client          pool.acquire
   6.4   0.5  order         Client          BEGIN
   6.9   0.6  order         Client          INSERT                      (orders)
   7.5   0.6  order         Client          copy_from "order_items"
   8.1   0.6  order         Client          INSERT                      (outbox_events)
   8.7   1.3  order         Client          COMMIT
   …  ~0.9 с спустя, асинхронно, тот же TraceId  …
 897.5   0.05 notification  Consumer  order.events.v1 receive
 897.6   2.4  notification  Consumer  order.events.v1 process
 897.7   …    notification  Client      BEGIN → INSERT → batch → COMMIT
```

SQL-спаны `otelpgx` называются по типу операции (`SELECT`, `INSERT`, `BEGIN`, `COMMIT`) — текст запроса
смотрите в атрибутах спана.

Как читать:

1. **Самый длинный дочерний спан** — первый подозреваемый.
2. **Промежутки без дочерних спанов** у родителя — время внутри самого сервиса (код, сериализация, GC).
3. **Последовательные вызовы**, которые могли бы идти параллельно, — потенциал оптимизации. В трейсе выше
   (снят до оптимизации) `GetUser`, `GetAddress` и `GetProducts` идут ступенькой, хотя не зависят друг
   от друга. Именно по этому трейсу их сделали параллельными: теперь три спана начинаются одновременно,
   а весь трейс стал вдвое короче.
4. `pool.acquire` заметной длины — ожидание соединения к БД (находка нагрузочного теста: 76 мс из 108).
5. Красный спан — ищите в его атрибутах `exception`/`status message`, затем логи этого трейса.

### 6.3. Асинхронная часть (модуль async-propagation)

notification не получает HTTP-заголовков: сообщение идёт через outbox → Debezium → Kafka. Поэтому
`order` при записи в outbox сохраняет `traceparent` текущего спана, Debezium переносит его в **заголовки
Kafka-сообщения**, а consumer в notification извлекает контекст и продолжает **тот же** трейс.
Результат: в одном трейсе видно и синхронный запрос, и обработку события, а разрыв между ними — это
фактический **лаг доставки** (≈ 0.8 с p95 под нагрузкой, в основном — задержка CDC).

Ещё одна деталь: `traceparent`, присланный внешним клиентом в gateway, **не** становится родителем —
он попадает в `Links`. gateway — граница доверия, внешний клиент не должен управлять нашими трейсами.

---

## 7. Связка логов и трейсов

- **Лог → трейс.** В Logs Explorer или Explore раскройте запись → у поля `TraceId` ссылка → откроется трейс.
- **Трейс → логи.** В открытом трейсе у спана есть кнопка логов → запрос к `otel_logs` по этому `TraceId`.
- В stdout сервисов (терминал `task watch-all`) у логов тоже есть `trace_id`/`span_id` — можно скопировать
  и найти трейс в Traces Explorer через `search`.

Эти переходы — штатный OTel-режим ClickHouse-датасорса, ничего настраивать не нужно.

---

## 8. Explore и SQL: когда дашбордов мало

**Explore → data source `clickhouse` → SQL Editor.** Полезные запросы:

```sql
-- Какие сервисы пишут трейсы и сколько
SELECT ServiceName, count() FROM otel.otel_traces
WHERE Timestamp > now() - INTERVAL 15 MINUTE
GROUP BY ServiceName;

-- Точный p95 по серверным спанам (сверка с p95 из гистограмм)
SELECT ServiceName, SpanName,
       quantile(0.95)(Duration) / 1e6 AS p95_ms, count() AS n
FROM otel.otel_traces
WHERE Timestamp > now() - INTERVAL 15 MINUTE AND SpanKind = 'Server'
GROUP BY ServiceName, SpanName ORDER BY p95_ms DESC;

-- Самые медленные трейсы gateway
SELECT TraceId, Duration / 1e6 AS ms, SpanName
FROM otel.otel_traces
WHERE ServiceName = 'gateway' AND SpanKind = 'Server'
  AND Timestamp > now() - INTERVAL 15 MINUTE
ORDER BY Duration DESC LIMIT 20;

-- Где время внутри медленных трейсов: сумма по имени спана
SELECT ServiceName, SpanName, sum(Duration) / 1e6 AS total_ms, count() AS n
FROM otel.otel_traces
WHERE TraceId IN (
  SELECT TraceId FROM otel.otel_traces
  WHERE ServiceName = 'gateway' AND SpanKind = 'Server' AND Duration > 50e6
    AND Timestamp > now() - INTERVAL 15 MINUTE)
GROUP BY ServiceName, SpanName ORDER BY total_ms DESC LIMIT 15;

-- Ошибки в логах
SELECT Timestamp, ServiceName, Body, TraceId
FROM otel.otel_logs
WHERE SeverityText = 'ERROR' AND Timestamp > now() - INTERVAL 1 HOUR
ORDER BY Timestamp DESC LIMIT 50;

-- Какие метрики вообще есть
SELECT MetricName, count() FROM otel.otel_metrics_histogram GROUP BY MetricName
UNION ALL
SELECT MetricName, count() FROM otel.otel_metrics_sum GROUP BY MetricName;
```

`Duration` в `otel_traces` — в наносекундах, отсюда `/ 1e6` для миллисекунд.

Макросы плагина, которые встречаются в SQL панелей: `$__timeFilter(col)` — фильтр по выбранному
диапазону; `$__timeInterval(col)` — округление до шага графика; `$__interval_s` — шаг в секундах;
`${service:singlequote}` — подстановка значения переменной.

---

## 9. Профили в Grafana (Pyroscope)

Alloy (`deploy/alloy/profiles.dev.alloy`) постоянно опрашивает `/debug/pprof` сервисов и отправляет
профили в Pyroscope. В Grafana: **Explore → data source Pyroscope** (или раздел Drilldown → Profiles).

- Выберите `service_name` и тип: `process_cpu`, `memory` (alloc/inuse), `goroutine`, `mutex`, `block`.
- **Flame graph**: ширина прямоугольника = доля ресурса, по вертикали — стек вызовов (снизу вызывающие,
  сверху вызываемые). Ищите **широкие плато наверху** — это функции, которые сами тратят много.
  Широкий, но «прозрачный» прямоугольник, на котором стоят другие, — просто вызывающий код.
- **Comparison / Diff**: выделите два периода (до и после нагрузки или до и после изменения) —
  красное выросло, зелёное уменьшилось.

Для load-стека профилирование включается флагом при подъёме: `LOAD_PROFILING=1 task load-up`
(добавляет Pyroscope и Alloy из `load/compose.profiling.yaml`, Pyroscope на `127.0.0.1:34040`).
`load-stress` и `load-soak` с работающим Alloy не стартуют: CPU-профайлер в процессе один, и
автоснимки задач конфликтовали бы с Alloy.

---

## 10. Сценарий расследования: «выросла задержка»

Пример по мотивам реальной находки нагрузочного теста (`load/results/baseline.md`).

1. **Services Overview → Сводка.** `error_rate` = 0, но p95 gateway вырос с 5 до 20 мс.
   Значит, не сбой, а замедление.
2. **gRPC server → p95 по методам.** Вырос p95 `CreateOrder` в order, а `GetUser`/`GetProducts` — нет.
   Значит, дело в order, а не в его зависимостях.
3. **Postgres (pgxpool).** Утилизация пула order упирается в 1.0, растут «Ожидание acquire» и
   «Empty acquire /s». Гипотеза: не хватает соединений.
4. **Runtime.** Горутины растут вместе с нагрузкой — запросы ждут в очереди за соединением.
5. **Traces Explorer**, `service=gateway`, `min_duration=50ms` → открыть несколько трейсов.
   В каждом длинный `pool.acquire` в order и долгий `COMMIT`. Гипотеза подтверждена на конкретных запросах.
6. **Explore → SQL** (запрос «Где время внутри медленных трейсов») — количественно: `pool.acquire` даёт ~70% времени.
7. **Pyroscope / pprof** — почему соединения держатся долго: CPU order не насыщен, значит, ждём БД
   (`COMMIT`, WAL), а не код.
8. **Вывод и проверка:** увеличить `PG_MAX_CONNS` у order, повторить `task load-stress`, сравнить с baseline.

Обратите внимание на порядок: **от общего к частному** — сводка → сервис → ресурс → конкретный запрос →
код. И каждое утверждение подтверждено вторым сигналом.

---

## 11. Чек-лист «как смотреть на дашборд»

1. Какой **диапазон времени** выбран? Не смотрите на «Last 24 hours», если проблема была 5 минут.
2. Какие **переменные** выбраны? `service = All` или конкретный?
3. Что **по осям** и в каких единицах (мс или с, RPS или штуки)?
4. Это **метрика** (точный счётчик) или **спаны** (то, что попало в трейсы)?
5. Где **норма**? Сравните с периодом без проблем (выделите и отзумьте).
6. Что изменилось **первым**? Причина обычно раньше следствия.
7. Подтвердите **вторым сигналом**: метрика ↔ трейс ↔ лог ↔ профиль.

---

## 12. Практика

1. Поднимите dev-окружение (раздел 1), создайте 10 заказов, откройте **Services Overview**.
   Объясните, почему `rps` у user примерно в 2 раза больше, чем у order.
2. Создайте заказ с несуществующим товаром. Где это видно: в `error_rate` или в `client_error_rate`? Почему?
3. В **Traces Explorer** найдите трейс своего заказа. Найдите спан notification. Сколько прошло между
   ответом gateway и обработкой в notification? Что съедает это время?
4. В **Logs Explorer** найдите лог этого запроса и перейдите из него в трейс. Затем обратно.
5. Остановите notification (`Ctrl+C` в его процессе Air), создайте 5 заказов, запустите снова.
   Посмотрите на «Kafka: fetch records /s» — видите «догоняющий» всплеск?
6. Откройте любую панель Services Overview в **Edit**, прочитайте SQL и объясните, что делает
   `sum(Count) / $__interval_s`.
7. Выполните сверочный запрос точного p95 (раздел 8) и сравните с p95 на панели. Почему они отличаются?
8. `task load-up && task load-stress`, откройте «Delivery / Load» на `http://localhost:38300`.
   Найдите ступень, на которой достигнутый RPS отстал от целевого, и объясните причину по панелям пула pgx.
9. В dev-окружении (Alloy поднят `task compose-up`) погенерируйте заказы пару минут, откройте Pyroscope
   для order и найдите три самые широкие функции в CPU flame graph.
