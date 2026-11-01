# Tasks: load

> Plan: [plan.md](plan.md) · Spec: [spec.md](spec.md)

## Definition of Done (для каждой задачи)

- Критерии приёмки задачи выполнены, проверки из «Verification» пройдены и их результат записан.
- `task lint` зелёный в затронутых модулях; `task test` зелёный.
- Версии всех новых образов и инструментов фиксированы (без `latest`).
- Боевой код сервисов не изменён (только новые `_test.go`); иначе — стоп и ask first.
- Никаких прогонов нагрузки по dev-стеку; сырьё не коммитится, кроме файлов, на которые ссылается `baseline.md`.
- Чекбокс задачи отмечен сразу после выполнения. Коммит — только по команде пользователя.

---

## Task 1: Load-стек и `load-up`/`load-down`

**Description:** Создать `load/compose.yaml` (проект `delivery-load`) на основе `e2e/compose.yaml`:
тот же состав плюс Grafana (provisioning и дашборды из `deploy/grafana`), порты `38xxx`,
`LOG_LEVEL=info`, `OTEL_ENABLED=true`, Postgres без томов, `deploy.resources.limits` на каждый
контейнер (сумма ≤ 8 CPU), профиль `tools` с сервисами `k6` и `ghz` (фиксированные версии).
`load/Taskfile.yml` с `load-up` (проверка образов → `up -d --build --wait`) и `load-down` (`down -v`);
корневой Taskfile делегирует `load-*`. `e2e-images-check` обобщается во внутреннюю задачу для обоих стеков.

**Acceptance criteria:**
- [ ] `task load-up` поднимает стек с нуля, все healthcheck зелёные, Debezium-коннектор зарегистрирован
- [ ] `task load-down` удаляет контейнеры и тома, повторный `load-up` стартует с пустыми БД
- [ ] Проверка образов ловит расхождение версии с корневым `compose.yaml` и для e2e, и для load; `task e2e` по-прежнему работает
- [ ] Порты не пересекаются с dev и e2e (оба стека можно поднять одновременно)

**Verification:**
- [ ] `task load-up && docker compose -f load/compose.yaml -p delivery-load ps` — все сервисы `healthy`/`running`, одноразовые — `exited (0)`
- [ ] `docker compose ... run --rm --entrypoint sh k6 -c 'wget -qO- http://gateway:8080/...'` (или curl-контейнер) — gateway отвечает из сети стека
- [ ] `docker compose -f load/compose.yaml config | grep -c 'cpus'` = числу долгоживущих контейнеров; сумма CPU ≤ 8
- [ ] Подменить тег одного образа в `load/compose.yaml` → `task load-up` падает с понятным сообщением; вернуть
- [ ] `task e2e` зелёный

**Dependencies:** None

**Files likely touched:**
- `load/compose.yaml`
- `load/Taskfile.yml`
- `Taskfile.yml`

**Estimated scope:** M

---

## Task 2: Seed и `load-smoke`, `task load`

**Description:** `load/sql/seed.sql` и одноразовый контейнер `seed-product` добавляют товары до ≥ 10
(после `migrate-product`). Задача получения seed-id в `load/Taskfile.yml` (`psql -At` в user/product
Postgres) передаёт `SEED_ADDRESS_ID`, `SEED_PRODUCT_IDS`, `GATEWAY_URL`, `PROFILE` в
`docker compose run --rm k6`. Сценарий `k6/create_order.js`, `k6/lib/config.js` (профиль `smoke`:
`constant-arrival-rate` 5 RPS × 1 мин, `checks` rate == 1, `abortOnFail`), `k6/lib/seed.js`
(`loadSeed()`, `randomOrder()`: 1–3 товара, количество 1–5). Проверки: `201` и `data.id` (см. Open
Questions плана). `task load-smoke`, `task load` (`defer` с `load-down`, `LOAD_KEEP=1` оставляет стек).

**Acceptance criteria:**
- [ ] `task load-smoke` на поднятом стеке проходит, `checks` = 100%, код выхода 0
- [ ] При сломанном запросе (например, пустой `SEED_ADDRESS_ID` или неверный путь) задача падает с кодом ≠ 0
- [ ] `task load` выполняет up → smoke → down; стек убирается и при падении smoke; `LOAD_KEEP=1` оставляет его

**Verification:**
- [ ] `task load` от чистого состояния — зелёный, `docker ps --filter name=delivery-load` пусто после
- [ ] `GATEWAY_URL=http://gateway:8080/nope task load-smoke` → код ≠ 0
- [ ] `SELECT count(*) FROM orders` в order-Postgres ≈ 300 после smoke; в заказах встречаются 1, 2 и 3 позиции

**Dependencies:** Task 1

**Files likely touched:**
- `load/sql/seed.sql`
- `load/compose.yaml`
- `load/k6/create_order.js`, `load/k6/lib/config.js`, `load/k6/lib/seed.js`
- `load/Taskfile.yml`, `Taskfile.yml`

**Estimated scope:** M

---

## Task 3: Метрики k6 в ClickHouse и дашборд `Load`

**Description:** Включить OTLP-вывод k6 в `otel-collector` load-стека (имя вывода и env — по
документации зафиксированной версии k6). Дашборд `deploy/grafana/dashboards/load.json`: RPS
целевой/достигнутый, p50/p95/p99 и доля ошибок k6 по `stage`, `dropped_iterations`; RED
gateway/order/user/product; runtime Go (`go.memory.used`, `go.goroutine.count`); пул pgx по каждому
сервису с БД (`acquired`/`max_connections`, скорость `empty_acquire` и `empty_acquire_wait_time`,
`canceled_acquires`, `acquire_duration`) — по [каталогу метрик](../../../specs/observability/metrics/metrics-catalog.md).

**Acceptance criteria:**
- [ ] Во время `load-smoke` метрики k6 появляются в ClickHouse и на дашборде `Load`
- [ ] На том же дашборде видны RED, runtime и пул pgx сервисов за тот же интервал
- [ ] Дашборд загружается provisioning-ом и в dev-стеке (корневой `compose.yaml`) без ошибок панелей

**Verification:**
- [ ] `LOAD_KEEP=1 task load` → Grafana load-стека, дашборд `Load`, все панели с данными за интервал smoke
- [ ] SQL в ClickHouse: метрики с префиксом k6 есть в `otel_metrics_*` за последний прогон
- [ ] Скриншот/описание панелей записаны в todo (результат проверки)

**Dependencies:** Task 2

**Files likely touched:**
- `load/compose.yaml`
- `load/Taskfile.yml`
- `deploy/grafana/dashboards/load.json`

**Estimated scope:** M

---

## Checkpoint 1: После задач 1–3
- [ ] `task load` зелёный от чистого состояния, стек убирается и при падении
- [ ] `task e2e`, `task lint`, `task test` зелёные
- [ ] Дашборд `Load` показывает k6 и сервисы в одном интервале
- [ ] Ревью с пользователем перед поиском предела

---

## Task 4: Профиль `stress`, разметка ступеней, сэмплер, разведочный прогон

**Description:** Профиль `stress` (`ramping-arrival-rate`, ступень = 30 с разгон + 90 с плато,
`abortOnFail` по критерию предела), тег `stage` по прошедшему времени сценария, `handleSummary`
пишет в `load/results/raw/<run>/summary.json` метрики по ступеням и время начала/конца каждой
ступени. Фоновый сэмплер `docker stats` (раз в 5 с → `docker-stats.csv`) на время прогона.
`task load-stress` (ступени переопределяются env для разведки). Разведочный прогон грубыми
ступенями ×2 (25 → 50 → 100 → …) до деградации; по нему выбрать старт, шаг и верх так, чтобы до
предела было 8–15 ступеней, записать в `config.js` и в todo.

**Acceptance criteria:**
- [ ] `task load-stress` пишет summary с p50/p95/p99, ошибками, достигнутым RPS и `dropped_iterations` по каждой ступени плюс границы ступеней
- [ ] Прогон останавливается по критерию предела (1% ошибок / p95 > 3× первой ступени / RPS < 95% или `dropped_iterations`), а не только по времени
- [ ] Разведка выполнена, выбранные числа ступеней в `config.js`, обоснование в todo

**Verification:**
- [ ] Разведочный прогон: summary и `docker-stats.csv` в `raw/`, деградация видна в summary
- [ ] Короткий stress с заведомо низким верхом (env) завершается без abort, со всеми ступенями в summary
- [ ] `load-smoke` после правок `k6/` зелёный

**Dependencies:** Task 2 (Task 3 — для наблюдения, не блокирует)

**Files likely touched:**
- `load/k6/lib/config.js`, `load/k6/create_order.js`, `load/k6/lib/summary.js`
- `load/Taskfile.yml`
- `.gitignore`

**Estimated scope:** M

---

## Task 5: Лаг асинхронной цепочки (`load-lag`, `rpk`)

**Description:** `load/sql/lag.sql`: из `notification-postgres` через `dblink` к `order-postgres`
(`CREATE EXTENSION IF NOT EXISTS dblink`) — лаг `notifications.created_at − orders.created_at`,
соединение через `inbox_events.aggregate_id` / `event_id`; p50/p95/max по ступеням (границы — из
summary последнего прогона, передаются параметрами). `task load-lag` выводит таблицу и пишет её в
`raw/<run>/lag.txt`. Сэмплер из Task 4 в конце каждой ступени снимает `rpk group describe notification`.

**Acceptance criteria:**
- [ ] `task load-lag` после smoke/stress выдаёт p50/p95/max лага по ступеням
- [ ] Число заказов без уведомления (не дошли) выводится отдельно
- [ ] Лаг группы `notification` по ступеням сохраняется в `raw/<run>/`

**Verification:**
- [ ] После `load-smoke`: одна ступень, все ~300 заказов с уведомлением, лаг в секундах правдоподобен
- [ ] Остановить `notification` на 20 с во время smoke (`docker compose stop/start`) → `load-lag` показывает рост лага, `rpk` — рост lag группы

**Dependencies:** Task 4

**Files likely touched:**
- `load/sql/lag.sql`
- `load/Taskfile.yml`

**Estimated scope:** S

---

## Task 6: gRPC-нагрузка ghz (`load-grpc`)

**Description:** Конфиги `load/ghz/<service>_<method>.json` для `order.v1.OrderService/CreateOrder`,
`product.v1.ProductService/GetProducts` (1 и 10 id), `user.v1.UserService/GetUser`,
`address.v1.AddressService/GetAddress`; `.proto` монтируются из `proto/proto`, без рефлексии.
Seed-id подставляются из env (Task 2). Ступени `--concurrency` 10/50/100/200 по 60 с без `--rps`.
`task load-grpc` (опционально `METHOD=`) пишет JSON по ступеням в `raw/<run>/ghz/` и сводную
таблицу RPS, p50/p95/p99, ошибки по кодам.

**Acceptance criteria:**
- [ ] Все 5 прогонов (4 метода, `GetProducts` дважды) завершаются, доля ответов `OK` на первой ступени ≥ 99%
- [ ] Сводная таблица по методам и ступеням формируется одной задачей
- [ ] ghz ходит по именам сервисов в сети стека, не через порты хоста

**Verification:**
- [ ] `task load-grpc METHOD=user_GetUser` — короткий прогон, JSON в `raw/`
- [ ] Полный `task load-grpc` — таблица по всем методам
- [ ] Payload с неверным id даёт `NotFound` в разбивке кодов (проверка, что payload реально уходит)

**Dependencies:** Task 2

**Files likely touched:**
- `load/ghz/*.json` (5 файлов)
- `load/Taskfile.yml`

**Estimated scope:** M

---

## Checkpoint 2: После задач 4–6
- [ ] Ступени `stress` выбраны по разведке и записаны
- [ ] `load-lag` и `load-grpc` дают таблицы
- [ ] `task lint`, `task test` зелёные
- [ ] Ревью с пользователем перед полным прогоном

---

## Task 7: Полный прогон, узкое место, `baseline.md`

**Description:** Чистый стек → `load-smoke` → `load-stress` (выбранные ступени) → `load-lag` →
`load-grpc`. На ступени перед пределом и на первой деградировавшей: дашборд `Load`, пул pgx,
`docker stats`, трейсы p99 из ClickHouse (какой спан доминирует). `load/results/baseline.md` по
структуре спеки, разделы 1–5 и 7 (окружение, сводка, таблица ступеней, ghz, узкое место с
доказательствами ≥ 2 источников и рекомендациями, как воспроизвести). В `raw/` оставить и
разрешить в `.gitignore` только файлы, на которые ссылается отчёт.

**Acceptance criteria:**
- [ ] Предел HTTP (или «не найден до 2000 RPS») и предел каждого gRPC-метода записаны с таблицами ступеней
- [ ] Узкое место названо и подтверждено минимум двумя источниками; если причина внутри сервиса не видна без профиля — так и сказано
- [ ] Лаг по ступеням и вывод, догоняет ли `notification`

**Verification:**
- [ ] Все ссылки `baseline.md` на `raw/` резолвятся, файлы под git
- [ ] Числа в таблице ступеней совпадают с summary JSON (выборочная сверка 2–3 ступеней)
- [ ] Окружение: CPU/RAM, ОС, Docker, версии образов, Go, лимиты, коммит — заполнено

**Dependencies:** Task 3, Task 4, Task 5, Task 6

**Files likely touched:**
- `load/results/baseline.md`
- `load/results/raw/<run>/*`
- `.gitignore`

**Estimated scope:** M

---

## Task 8: Профиль `regression` и `load-regression`

**Description:** Профиль `regression`: `constant-arrival-rate` = 70% предела из baseline, 5 мин;
`thresholds`: p95 ≤ 1.5× p95 baseline на этом RPS, `http_req_failed` < 1%, `checks` == 1.
Числа — в `config.js`, порог p95 переопределяется env `REGRESSION_P95_MS` для проверки гейта.
Раздел 6 `baseline.md`.

**Acceptance criteria:**
- [ ] `task load-regression` на текущем коде проходит (код 0)
- [ ] С заведомо заниженным порогом падает (код ≠ 0), результат проверки записан в todo
- [ ] Раздел 6 `baseline.md` совпадает с числами в `config.js`

**Verification:**
- [ ] `task load-regression` — зелёный
- [ ] `REGRESSION_P95_MS=1 task load-regression` — красный, k6 указывает на нарушенный threshold

**Dependencies:** Task 7

**Files likely touched:**
- `load/k6/lib/config.js`
- `load/Taskfile.yml`
- `load/results/baseline.md`

**Estimated scope:** S

---

## Task 9: Профиль `soak` и `load-soak`

**Description:** Профиль `soak`: тот же RPS, 30 мин, разметка «первые 5 мин / последние 5 мин» в
summary. `load/sql/soak-snapshot.sql`: `pg_total_relation_size` для `outbox_events`,
`inbox_events`, `notifications`, состояние `pg_replication_slots` (`confirmed_flush_lsn`, отставание
WAL) — снимок в начале и в конце. `task load-soak` с сэмплером. Раздел soak в `baseline.md` по
таблице спеки: память и горутины, пул pgx, p99 начало/конец (порог 1.5×), лаг, таблицы, WAL — и вывод
об утечках. Найденная проблема — фиксируется с доказательством, исправление не делается (ask first).

**Acceptance criteria:**
- [ ] `task load-soak` проходит 30 мин без abort
- [ ] Отчёт сравнивает начало и конец по всем строкам таблицы «Soak» спеки
- [ ] Есть явный вывод: утечки/рост есть или нет, с доказательствами

**Verification:**
- [ ] Снимки SQL начала и конца в `raw/<run>/`
- [ ] Тренды памяти/горутин/пула за 30 мин видны на дашборде `Load`

**Dependencies:** Task 7 (Task 8 — тот же RPS)

**Files likely touched:**
- `load/k6/lib/config.js`
- `load/sql/soak-snapshot.sql`
- `load/Taskfile.yml`
- `load/results/baseline.md`

**Estimated scope:** M

---

## Checkpoint 3: После задач 7–9
- [ ] `baseline.md` закрывает разделы 1–7 и soak
- [ ] `load-regression` зелёный и падает на заниженном пороге
- [ ] Ревью с пользователем (ask first: перезапись baseline, найденные проблемы)

---

## Task 10: Инструменты бенчмарков и бенчмарки gateway

**Description:** `golang.org/x/perf/cmd/benchstat` как `tool` в `e2e/go.mod`. Задачи в
`load/Taskfile.yml` (делегируются из корня): `bench` (`go test -run='^$' -bench=. -benchmem
-count=10` по модулям с бенчмарками → `load/bench/new.txt`), `bench-compare` (`go tool benchstat
baseline.txt new.txt`), `bench-baseline` (копия new → baseline). `new.txt` в `.gitignore`.
Первые бенчмарки: `gateway/.../transport/http/v1/order` — разбор JSON + `Validate`, маппинг в gRPC
(подбенчмарки `items=1`, `items=10`), `b.Loop()`, имена `Benchmark<Тип>_<Метод>`.

**Acceptance criteria:**
- [ ] `task bench` пишет `load/bench/new.txt` с бенчмарками gateway (`ns/op`, `B/op`, `allocs/op`)
- [ ] `task bench-compare` работает (на первом шаге — `new.txt` против самого себя)
- [ ] `task test` бенчмарки не запускает, `go vet` и линтер их проверяют

**Verification:**
- [ ] `task bench && task bench-compare`
- [ ] `task -d services/gateway lint test` зелёные; время `task test` до/после записано

**Dependencies:** None

**Files likely touched:**
- `e2e/go.mod`, `e2e/go.sum`
- `load/Taskfile.yml`, `Taskfile.yml`, `.gitignore`
- `services/gateway/internal/transport/http/v1/order/bench_test.go`

**Estimated scope:** M

---

## Task 11: Бенчмарки `platform`

**Description:** Бенчмарки `platform/http/middleware` (полная цепочка middleware на `httptest`),
`platform/http/response` (`Created`/`Fail` — запись JSON-конверта), `platform/grpc/interceptors`
(цепочка интерсепторов с пустым handler). Модуль `platform` добавляется в список `task bench`.

**Acceptance criteria:**
- [ ] Бенчмарки трёх пакетов в `new.txt`
- [ ] Бенчмарки без I/O и без сети, результат стабилен (разброс `benchstat` ≤ ~5%)

**Verification:**
- [ ] `task bench && task bench-compare` — разброс в выводе приемлемый
- [ ] `task -d platform lint test` зелёные

**Dependencies:** Task 10

**Files likely touched:**
- `platform/http/middleware/bench_test.go`
- `platform/http/response/bench_test.go`
- `platform/grpc/interceptors/bench_test.go`
- `load/Taskfile.yml`

**Estimated scope:** S

---

## Task 12: Бенчмарки `order`, `notification` и `baseline.txt`

**Description:** Бенчмарки `order/internal/domain` (создание заказа и расчёт суммы, `items=1/10`),
`order/internal/service` (`CreateOrder` с моками mockery и фейковым `TxManager`),
`order/internal/event` (сериализация события в outbox), `notification/.../transport/kafka/order`
(разбор payload события по эталону `contracts/events/order.created.json`). Затем `task bench` по
всем модулям и `task bench-baseline` → закоммитить `load/bench/baseline.txt` (ask first перед записью).

**Acceptance criteria:**
- [ ] Покрыты все пакеты из таблицы «Go-бенчмарки» спеки
- [ ] `load/bench/baseline.txt` содержит все бенчмарки, `bench-compare` против него без значимых изменений на том же коде

**Verification:**
- [ ] `task bench && task bench-compare` — `~` (нет значимых изменений) по всем строкам
- [ ] `task lint`, `task test` зелёные; время `task test` не выросло

**Dependencies:** Task 11

**Files likely touched:**
- `services/order/internal/domain/bench_test.go`
- `services/order/internal/service/bench_test.go`
- `services/order/internal/event/bench_test.go`
- `services/notification/internal/transport/kafka/order/bench_test.go`
- `load/bench/baseline.txt`

**Estimated scope:** M

---

## Checkpoint: Complete
- [ ] Все Success Criteria [спеки](spec.md) отмечены
- [ ] `task lint`, `task test`, `task e2e` зелёные
- [ ] «Итоги модуля `load`» в [map.md](../../../specs/testing/map.md); профилирование — ссылка на `observability/profiling`
- [ ] Завершение по `spec-workflow` (статус `done`, публикация, архив, ссылки) — после подтверждения пользователя
