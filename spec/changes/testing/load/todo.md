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
- [x] `task load-up` поднимает стек с нуля, все healthcheck зелёные, Debezium-коннектор зарегистрирован
- [x] `task load-down` удаляет контейнеры и тома, повторный `load-up` стартует с пустыми БД
- [x] Проверка образов ловит расхождение версии с корневым `compose.yaml` и для e2e, и для load; `task e2e` по-прежнему работает
- [x] Порты не пересекаются с dev и e2e (оба стека можно поднять одновременно)

**Verification:**
- [x] `task load-up && docker compose -f load/compose.yaml -p delivery-load ps` — все сервисы `healthy`/`running`, одноразовые — `exited (0)`
- [x] `docker compose ... run --rm --entrypoint sh k6 -c 'wget -qO- http://gateway:8080/...'` (или curl-контейнер) — gateway отвечает из сети стека
- [x] `docker compose -f load/compose.yaml config | grep -c 'cpus'` = числу долгоживущих контейнеров; сумма CPU ≤ 8
- [x] Подменить тег одного образа в `load/compose.yaml` → `task load-up` падает с понятным сообщением; вернуть
- [x] `task e2e` зелёный

**Результат проверки:** `load-up` с нуля — 14 долгоживущих контейнеров healthy/running, 5 одноразовых
`Exited (0)`, коннектор `order-outbox` RUNNING (~1.5 мин с загрузкой образов). Из сети стека
`gateway:8080` отвечает (404 на `/nope`). 14 блоков `cpus` = 14 контейнеров, сумма 7.75 CPU; память
по лимитам ≈ 6.3 ГБ (Docker Desktop даёт 7.65 ГБ, а не 16 ГБ из спеки — учесть в отчёте и при k6).
Подмена тега redpanda в `load/` и в `e2e/` → понятное сообщение, код 201; `load-down` убирает
контейнеры и тома; повторный `load-up` — `orders` = 0. `task e2e` зелёный при поднятом load-стеке
(порты 38xxx не пересекаются). `task lint`, `task test` зелёные. `ghz` (`obvionaoe/ghz:v0.117.0`) —
только linux/amd64, на Apple Silicon идёт через эмуляцию (`platform: linux/amd64`), отметить в отчёте.

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
(`loadSeed()`, `randomOrder()`: 1–3 товара, количество 1–5). Проверки: `201` и `data.id`.
`task load-smoke`, `task load` (`defer` с `load-down`, `LOAD_KEEP=1` оставляет стек).

**Acceptance criteria:**
- [x] `task load-smoke` на поднятом стеке проходит, `checks` = 100%, код выхода 0
- [x] При сломанном запросе (например, пустой `SEED_ADDRESS_ID` или неверный путь) задача падает с кодом ≠ 0
- [x] `task load` выполняет up → smoke → down; стек убирается и при падении smoke; `LOAD_KEEP=1` оставляет его

**Verification:**
- [x] `task load` от чистого состояния — зелёный, `docker ps --filter name=delivery-load` пусто после
- [x] `GATEWAY_URL=http://gateway:8080/nope task load-smoke` → код ≠ 0
- [x] `SELECT count(*) FROM orders` в order-Postgres ≈ 300 после smoke; в заказах встречаются 1, 2 и 3 позиции

**Результат проверки:** RED — до правок `task load-smoke` не существовал (`Task "load-smoke" does not exist`,
код 200). Smoke на поднятом стеке: 300 запросов, `checks` 600/600 = 100%, p95 ≈ 12 мс, код 0.
`GATEWAY_URL=.../nope` → `checks` 0%, прогон прерван `abortOnFail` через ~2 с, код 201; пустой
`SEED_ADDRESS_ID` и неизвестный `PROFILE` → исключение в `setup()`/`profile()`, код 107.
`task load` от нуля: штатно — код 0, контейнеров и томов `delivery-load` после 0; со сломанным smoke —
код 201, стек и тома тоже убраны; `LOAD_KEEP=1` оставил стек с сообщением. После smoke: `orders` = 300,
заказов с 1/2/3 позициями 99/107/94, `quantity` 1–5, товаров 12 (повторный `seed-product` — `INSERT 0 0`),
коннектор `order-outbox` RUNNING, `notifications` = 300. `task lint` (0 issues), `task test` зелёные;
боевой код сервисов не менялся.

**Найденный дефект Task 1 (исправлен в этой задаче):** первый `load-up` упал — `debezium` стал
unhealthy и был убит `OOMKilled` (код 137). Образ запускает JVM с `-Xmx2G`, лимит контейнера в
load-стеке 1 ГБ: куча росла за лимит, JVM зависала на GC (один плагин сканировался ~5 мин), cgroup
убивала процесс. На прогоне Task 1 куча до лимита не дошла — дефект плавающий. Исправление:
`KAFKA_HEAP_OPTS: "-Xms256M -Xmx512M"` у `debezium` в `load/compose.yaml`; лимиты ресурсов не менялись.
После него `load-up` стабильно проходит (~2.5 мин с нуля), debezium ≈ 630 МБ из 1 ГБ.

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
- [x] Во время `load-smoke` метрики k6 появляются в ClickHouse и на дашборде `Load`
- [x] На том же дашборде видны RED, runtime и пул pgx сервисов за тот же интервал
- [x] Дашборд загружается provisioning-ом и в dev-стеке (корневой `compose.yaml`) без ошибок панелей

**Verification:**
- [x] `LOAD_KEEP=1 task load` → Grafana load-стека, дашборд `Load`, все панели с данными за интервал smoke
- [x] SQL в ClickHouse: метрики с префиксом k6 есть в `otel_metrics_*` за последний прогон
- [x] Скриншот/описание панелей записаны в todo (результат проверки)

**Результат проверки:** RED — до прогона в `otel_metrics_*` 0 строк `k6_%`. k6 2.3.0 пишет в
`otel-collector` встроенным выводом `opentelemetry` (env `K6_OUT=opentelemetry`, `K6_OTEL_*`,
префикс `k6_`, `service.name=k6`, экспорт раз в 5 с) — без изменений `deploy/otel-collector`. По умолчанию
k6 шлёт кумулятивные счётчики/гистограммы; `OTEL_EXPORTER_OTLP_METRICS_TEMPORALITY_PREFERENCE=delta`
переводит их в приращения, как у сервисов. После smoke в ClickHouse: sum 7, gauge 3, histogram 8 метрик `k6_*`.
Дашборд `Delivery / Load` (`deploy/grafana/dashboards/load.json`, 20 элементов, 22 запроса): k6 (RPS целевой/
достигнутый, p50/p95/p99, ошибки http_req_failed и checks, dropped_iterations и VU, таблица по `stage`),
RED gateway/order/user/product, `go.memory.used`, горутины, пул pgx (утилизация, `empty_acquire`,
`empty_acquire_wait_time`, `canceled_acquires`, `acquire_duration`). Через `/api/ds/query` Grafana load-стека
после `LOAD_KEEP=1 task load` все 22 запроса без ошибок, все панели с данными (кроме `dropped_iterations`
и таблицы `stage` — см. ниже). В dev-Grafana (корневой compose, отдельный проект) дашборд загрузился,
22 запроса на пустых данных — без ошибок. Принудительный сценарий (200 RPS на 1 VU, неверный путь): панели
показали error rate 100% и `dropped_iterations`. Нюансы: сервисы экспортируют раз в 10 с, поэтому у сервисных
панелей шаг ≥ 10 с (у k6 — 5 с); перцентили k6 — по грубым бакетам гистограммы (точные — в summary.json, T4);
`dropped_iterations` у k6 появляется только при пропусках (нет данных = пропусков не было); тег `stage`
появится в T4 (до того одна строка «—»); `target_rps` — gauge из сценария, считается на init
(`currentTargetRate`), т.к. в VU объект options уже изменён k6. Скриншота нет (нет браузера), панели проверены API.
`task lint` (0 issues), `task test` зелёные; боевой код сервисов не менялся.

**Замечание вне задачи:** если итерация k6 падает исключением (так было при ошибке в `targetRate`), проверок
нет, `checks rate == 1` не нарушается и smoke возвращает код 0. Для T4/T8 стоит добавить порог на минимум
итераций/`checks` count. Не исправлялось.

**Dependencies:** Task 2

**Files likely touched:**
- `load/compose.yaml`
- `load/k6/create_order.js`, `load/k6/lib/config.js`
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
- [x] `task load-stress` пишет summary с p50/p95/p99, ошибками, достигнутым RPS и `dropped_iterations` по каждой ступени плюс границы ступеней
- [x] Прогон останавливается по критерию предела (1% ошибок / p95 > 3× первой ступени / RPS < 95% или `dropped_iterations`), а не только по времени
- [x] Разведка выполнена, выбранные числа ступеней в `config.js`, обоснование в todo

**Verification:**
- [x] Разведочный прогон: summary и `docker-stats.csv` в `raw/`, деградация видна в summary
- [x] Короткий stress с заведомо низким верхом (env) завершается без abort, со всеми ступенями в summary
- [x] `load-smoke` после правок `k6/` зелёный

**Результат проверки:** Разведка `STRESS_RATES=25,50,100,200,400,800,1600` (11 мин): до 400 RPS ступени чистые
(p95 5.5 → 2.9 → 3.6 мс, ошибок 0, достигнутый RPS = целевому), на 800 RPS p95 15.9 мс (> 3× эталона 5.5 мс),
порог `slow` остановил прогон (`verdict=limit_reached`), 1600 не достигнут. Предел лежит в 400–800 RPS.
Выбрано: старт 100, шаг 50, верх 1000 RPS (`config.js`) — до предела 8–15 ступеней; если предел
не найден до 1000, верх поднять. Короткий `STRESS_RATES=5,10` — 4 мин, без abort, обе ступени в summary.
`load-smoke` после правок: 300 запросов, checks 100%. `task lint` (0 issues), `task test` зелёные; боевой код не менялся.
Сырьё: `load/results/raw/<run>/{summary.json,docker-stats.csv}` (в `.gitignore`).

**Как устроен критерий (отступления от буквы спеки, на согласование):**
- p95 > 3× p95 первой ступени: порогам нужно статическое число, поэтому эталон — p95 200 запросов в `setup()`
  в темпе первой ступени (не сама первая ступень); порог — доля запросов медленнее 3× эталона > 5% (⇔ p95 > 3× эталона).
  Точный пост-расчёт по p95 первой ступени — в `summary.json` (`p95_ratio_to_first`).
- RPS < 95%: порогом не выразить; abort — по `dropped_iterations` (у них нет тега `stage`, ступень определяется по
  времени остановки), доля RPS считается в summary.
- Код k6 99 для `load-stress` — штатный исход («предел найден»); ошибка — вердикт `failed` в summary
  (исключение в итерациях, чужой порог, нет summary).
- Закрыто замечание Task 3: счётчик `checked_iterations` (порог `count>0`, сверка с `iterations` в summary).
- `task load` пока без stress (30+ мин); подключить после Task 7.

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
- [x] `task load-lag` после smoke/stress выдаёт p50/p95/max лага по ступеням
- [x] Число заказов без уведомления (не дошли) выводится отдельно
- [x] Лаг группы `notification` по ступеням сохраняется в `raw/<run>/`

**Verification:**
- [x] После `load-smoke`: одна ступень, все ~300 заказов с уведомлением, лаг в секундах правдоподобен
- [x] Остановить `notification` на 20 с во время smoke (`docker compose stop/start`) → `load-lag` показывает рост лага, `rpk` — рост lag группы

**Результат проверки:** `load-smoke` (без summary — одна ступень 0 = окно сэмплера): 301/301 заказов с уведомлением,
лаг p50 0.56 / p95 0.89 / max 0.99 с. Остановка `notification` на 20 с во время smoke: p95 лага 18.6 с, max 21.6 с,
`max_lag` группы 98 сообщений, к концу 0 — растёт и догоняет. `STRESS_RATES=5,10`: ступени 450 и 900 заказов, все
с уведомлением, лаг ~0.5/0.9 с. Лаг считается по плато ступени.
Как устроено: `sql/lag.sql` в `notification-postgres` + `dblink` к `order-postgres`, summary.json и `group-lag.csv`
передаются в psql переменными; сэмплер `scripts/group-lag.sh` пишет `TOTAL-LAG` группы раз в 5 с (smoke — 2 с),
а не снимок «в конце ступени», — по CSV берутся max и значение на конце ступени. `load-smoke` теперь тоже создаёт
`raw/<run>/`. Попутно исправлено: сэмплеры не гасились (`$!` в интерпретаторе Task даёт неверный PID, `docker-stats.sh`
из Task 4 утекал) — теперь `pkill -f` по пути прогона. `task lint`, `task test` зелёные; боевой код не менялся.

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
- [x] Все 5 прогонов (4 метода, `GetProducts` дважды) завершаются, доля ответов `OK` на первой ступени ≥ 99%
- [x] Сводная таблица по методам и ступеням формируется одной задачей
- [x] ghz ходит по именам сервисов в сети стека, не через порты хоста

**Verification:**
- [x] `task load-grpc METHOD=user_GetUser` — короткий прогон, JSON в `raw/`
- [x] Полный `task load-grpc` — таблица по всем методам
- [x] Payload с неверным id даёт `NotFound` в разбивке кодов (проверка, что payload реально уходит)

**Результат проверки:** RED — до правок `task load-grpc` не существовал. Короткий прогон `METHOD=user_GetUser`: JSON в
`raw/<run>/ghz/`, сводка в `summary.txt`. Неверный `USER_ID` → `NotFound=11122` (payload реально уходит). Полный
`task load-grpc` (5 конфигов × ступени 10/50/100/200 × 60 с, ~20 мин): `OK` 100% на всех ступенях, других кодов нет.
Пределы по RPS (плато): GetUser ≈ 5.0k, GetAddress ≈ 4.9k, GetProducts(1) ≈ 4.9–4.9k (на c200 падает до 4.2k),
GetProducts(10) ≈ 3.4k, CreateOrder ≈ 1.9–2.2k (p95 растёт 35→137 мс). Как устроено: конфиги ghz — шаблоны с `__USER_ID__`/
`__PRODUCT_n__`, `scripts/grpc.sh` рендерит их через `jq` под ступень и seed-id; ghz ходит на `user:8091`, `product:8092`,
`order:8090` внутри сети стека; `duration-stop: wait`, иначе in-flight запросы на конце ступени дают `Unavailable` (= concurrency).
В `compose.yaml` ghz получил монтирование `third_party/protovalidate` для `buf/validate`. p99 ≈ 70 мс на всех методах при
низкой нагрузке — вероятно, артефакт первых соединений/прогрева, проверить в Task 7. `task lint`, `task test` зелёные; боевой код не менялся.

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
