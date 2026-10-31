# Tasks: e2e

> Plan: [plan.md](plan.md) · Spec: [spec.md](spec.md)

Общее для каждой задачи (Definition of Done, в задачах не повторяется):
- `//go:build e2e` на каждом файле `e2e/`, кроме `env.go`; имена файлов `*_e2e_test.go`;
- testify, английские `name`, `t.Parallel` везде, кроме тестов устойчивости (`//nolint:paralleltest`);
  асинхронность только через `require.EventuallyWithT`, `time.Sleep` — только `stableWindow`;
- тест ходит только в HTTP `gateway`; БД — только `SELECT` с `default_transaction_read_only=on`,
  gRPC — только health, ClickHouse — только чтение; адреса только из `env.go`;
- каждый тест проверяет только свои заказы (по id); общие счётчики таблиц не используются;
- найденный баг: сначала красный тест, исправление боевого кода — только после согласия; баги в резюме задачи;
- `task test`, `task lint` зелёные (с T5 — и `task e2e`); `git commit` — только по команде.

---

## Phase 1: Фундамент

## Task 1: gRPC health в `platform/grpcserver`

**Description:** `grpcserver.New` регистрирует `health.NewServer()` (общий статус `SERVING`);
`graceful()` перед `GracefulStop` вызывает `Shutdown()`, переводя все сервисы в `NOT_SERVING`.
Единственное изменение боевого кода модуля. Телеметрия health уже отфильтрована (`filters.HealthCheck()`).

**Acceptance criteria:**
- [x] После `Serve` `Check` с пустым `service` возвращает `SERVING`
- [x] Во время graceful stop статус `NOT_SERVING` (тест через внутренний доступ к health-серверу или через `Check` до закрытия соединения)
- [x] Покрытие `grpcserver` не ниже текущих 91.7%, `goleak` в пакете зелёный

**Verification:**
- [x] `task -d platform test`
- [x] `task -d platform lint`
- [x] `task test-integration` (smoke-тесты `app` сервисов не сломаны)

**Dependencies:** None

**Files likely touched:**
- `platform/grpc/grpcserver/server.go`
- `platform/grpc/grpcserver/server_test.go`

**Estimated scope:** S

## Task 2: Общий Dockerfile сервисов и `.dockerignore`

**Description:** `deploy/docker/service.Dockerfile` с `ARG SERVICE`, контекст — корень репозитория.
Сборка в workspace (`go.work`): сначала `go.work*` и все `go.mod`/`go.sum` модулей из `use`,
`go mod download`, затем исходники; cache mounts BuildKit; `CGO_ENABLED=0 go build -trimpath`.
Финальный образ `gcr.io/distroless/static-debian12:nonroot`. `.dockerignore`: `bin/`, `tmp/`,
`coverage/`, `.git/`, `spec/`, `**/.env`.

**Acceptance criteria:**
- [x] Образ собирается для каждого из пяти сервисов
- [x] Контейнер стартует и завершается понятной ошибкой конфигурации без env (бинарь рабочий, пользователь nonroot)
- [x] Повторная сборка после правки `.go` не перекачивает модули (слой `go mod download` из кеша)

**Verification:**
- [x] `for s in gateway order user product notification; do docker build -f deploy/docker/service.Dockerfile --build-arg SERVICE=$s -t delivery-$s:e2e . || break; done`
- [x] `docker run --rm delivery-order:e2e` — выход с ошибкой `APP_NAME` required
- [x] `docker inspect -f '{{.Config.User}}' delivery-order:e2e` → `nonroot` (или `65532`)

**Dependencies:** None (T3 потом добавит `e2e/go.mod` в список `COPY`)

**Files likely touched:**
- `deploy/docker/service.Dockerfile`
- `.dockerignore`

**Estimated scope:** S

## Task 3: Каркас модуля `e2e/` в workspace и Taskfile

**Description:** Модуль `github.com/maksimegorovdev/delivery-backend/e2e`: `go.mod` (testify, pgx,
grpc — версии как в сервисах), `Taskfile.yml` (`tools`, `lint`, `test` — компиляция без тега,
`tidy`), `.golangci.yml` как у сервисов, `env.go` с адресами стека (константы портов e2e,
переопределение через env). `go.work` получает `./e2e`; корневой `MODULES` — `e2e`
(не `COVER_MODULES`, не `FUZZ_MODULES`). Dockerfile из T2 копирует `e2e/go.mod`.

**Acceptance criteria:**
- [x] `task test`, `task lint`, `task tidy`, `task tools` по корню проходят и включают `e2e`
- [x] `go vet -tags=e2e ./...` в `e2e/` компилирует пустой пакет тестов
- [x] `task cover` не трогает `e2e`; сборка образов из T2 по-прежнему проходит

**Verification:**
- [x] `task test && task lint`
- [x] `cd e2e && go vet -tags=e2e ./...`
- [x] `docker build -f deploy/docker/service.Dockerfile --build-arg SERVICE=gateway .`

**Dependencies:** None

**Files likely touched:**
- `e2e/go.mod`, `e2e/go.sum`
- `e2e/Taskfile.yml`, `e2e/.golangci.yml`, `e2e/env.go`
- `go.work`, `Taskfile.yml`

**Estimated scope:** M

## Checkpoint: Фундамент
- [ ] `task test`, `task lint`, `task test-integration` зелёные
- [ ] Пять образов собираются, `e2e/` в workspace
- [ ] Ревью с человеком

---

## Phase 2: Стек

## Task 4: `e2e/compose.yaml` и задачи стека

**Description:** Стек проекта `delivery-e2e` по разделу «Стек» спека: 4 Postgres (`order` с
`wal_level=logical`), `redpanda`, `debezium`, `clickhouse`, `otel-collector` (конфиги смонтированы
из `../deploy/...`), без томов, без `container_name`, без console/grafana; healthchecks; по одному
`migrate-<svc>` (`migrate/migrate:v4.19.1`, миграции смонтированы из `../services/<svc>/migrations/pg`);
`debezium-register` (`curlimages/curl`, фиксированный тег, `PUT` из `../deploy/debezium/order-outbox.json`,
падает при не-2xx); пять сервисов из `deploy/docker/service.Dockerfile` с env по именам compose,
`OTEL_ENABLED=true`, `LOG_FORMAT=json`, стартуют после своей миграции. Порты хоста — таблица спека.
Корневые задачи: `e2e-images-check`, `e2e-up` (сначала images-check, затем `up -d --build --wait`),
`e2e-down` (`down -v`), `e2e-logs`. Тег `curlimages/curl` вписать в «Tech Stack» спека.

**Acceptance criteria:**
- [x] `task e2e-up` завершается успешно: миграции и регистрация коннектора `exited (0)`, инфраструктура `healthy`, сервисы `running`
- [x] При запущенном `task compose-up` стек e2e поднимается без конфликтов портов и имён
- [x] `task e2e-down` не оставляет контейнеров, сетей и томов проекта `delivery-e2e`
- [x] Изменение тега образа в `e2e/compose.yaml` относительно корня роняет `task e2e-up` на `e2e-images-check`

**Verification:**
- [x] `task e2e-up && docker compose -p delivery-e2e ps -a`
- [x] `curl -s localhost:28083/connectors/order-outbox/status` → `RUNNING`
- [x] Ручной заказ: `curl -X POST localhost:28080/api/v1/orders ...` → `201`, строка в `notifications` (`psql` на 25435)
- [x] `task e2e-down && docker ps -a --filter label=com.docker.compose.project=delivery-e2e` пусто; `docker volume ls` без томов проекта
- [x] Порча тега → `task e2e-up` красный, возврат → зелёный

**Dependencies:** T1, T2, T3

**Files likely touched:**
- `e2e/compose.yaml`
- `Taskfile.yml`
- `spec/changes/testing/e2e/spec.md` (тег `curlimages/curl`)

**Estimated scope:** M

## Checkpoint: Стек
- [ ] Стек поднимается от чистого состояния, ручной заказ доходит до `notification`
- [ ] Dev-стек и e2e-стек работают одновременно
- [ ] Ревью с человеком

---

## Phase 3: Сквозной путь

## Task 5: `TestMain`, хелперы и `TestCreateOrder_HappyPath`

**Description:** Тест выявил расхождение боевого кода с контрактом: тип события `OrderCreated` вместо `order.created` и суммы строками в payload. Исправлено в `order` и `notification` (см. «Изменения конфигурации» спека). `stack_e2e_test.go`: `TestMain` с общим таймаутом 2 мин — коннектор `RUNNING`,
health `SERVING` у `order`/`user`/`product`, `400` от `gateway` на пустое тело, seed-id `SELECT`-ом
(адрес по email `demo@example.com`, товары по имени); при провале печатает шаг и `os.Exit(1)`.
`helpers_e2e_test.go`: HTTP-клиент `gateway` с разбором конверта, read-only пулы БД, константы
таймаутов. `order_e2e_test.go`: happy path по таблице «Сценарии». Задачи `e2e-test` и `e2e`
(up → test → down через `defer`, `E2E_KEEP=1` пропускает down).

**Acceptance criteria:**
- [x] Happy path: `201`, ответ согласован с seed (пользователь, адрес, имена, цены, `total_amount`); ровно одна строка `inbox_events` (`order.created`) и одна `notifications` (`channel='log'`); `payload` совпадает с ответом API по `order_id`, `user_id`, `items`, `total_amount`, `delivery_address`
- [x] Остановленный `order` → `TestMain` падает с сообщением о шаге health, тесты не запускаются
- [x] `task e2e` убирает стек и при красном тесте; `E2E_KEEP=1 task e2e` оставляет его

**Verification:**
- [x] `task e2e` зелёный от чистого состояния
- [x] `task e2e-up && docker compose -p delivery-e2e stop order && task e2e-test` → понятная ошибка `TestMain`
- [x] `task -d e2e lint`

**Dependencies:** T4

**Files likely touched:**
- `e2e/stack_e2e_test.go`
- `e2e/helpers_e2e_test.go`
- `e2e/order_e2e_test.go`
- `Taskfile.yml`

**Estimated scope:** M

## Task 6: `TestCreateOrder_ClientErrors` и `TestCreateOrder_NotFound`

**Description:** Табличные тесты в `order_e2e_test.go`. `400` + код ошибки в конверте: невалидный
JSON, нет `items`, `address_id` не UUID, `quantity = 0`. `404`: несуществующий товар (валидный UUID),
несуществующий адрес — ошибка из `product`/`user` через `order` и `gateway`. Ожидаемые коды ошибок
берутся из `platform/apperr` и маппинга `gateway`, не из фактического ответа.

**Acceptance criteria:**
- [ ] Все шесть кейсов проверяют HTTP-статус и `error.code` конверта
- [ ] Для `404` заказ не создан: в БД `notification` нет событий за время теста по этим запросам (проверяется отсутствием `201`, без общих счётчиков)

**Verification:**
- [ ] `task e2e-test` на поднятом стеке
- [ ] `task -d e2e lint`

**Dependencies:** T5

**Files likely touched:**
- `e2e/order_e2e_test.go`

**Estimated scope:** S

## Checkpoint: Сквозной путь
- [ ] `task e2e` зелёный, повторный запуск тоже
- [ ] Ревью с человеком

---

## Phase 4: Трейс и устойчивость

## Task 7: `TestTrace_EndToEnd`

**Description:** `trace_e2e_test.go`: случайный `trace_id` в `traceparent` запроса. Через HTTP
ClickHouse (`grafana_reader`) в `otel.otel_traces` найти корневой спан `gateway`
(`ParentSpanId = ''`, `has(Links.TraceId, <клиентский id>)`), затем под его `TraceId` — спаны
`gateway`, `order`, `user`, `product`, `notification`; спан `notification` не корень. Ни одного
спана с `TraceId` клиента. Ожидание — `EventuallyWithT` с `traceTimeout`. Перед написанием
запроса сверить схему (`DESCRIBE otel.otel_traces`).

**Acceptance criteria:**
- [ ] Найдены спаны всех пяти `ServiceName` под одним `TraceId`
- [ ] Спан `notification` имеет непустой `ParentSpanId`
- [ ] Спанов с клиентским `TraceId` нет (граница `WithPublicEndpoint` работает)

**Verification:**
- [ ] `task e2e-test` на поднятом стеке
- [ ] `task -d e2e lint`

**Dependencies:** T5

**Files likely touched:**
- `e2e/trace_e2e_test.go`
- `e2e/helpers_e2e_test.go`

**Estimated scope:** S

## Task 8: `TestResilience_NotificationDown` и `TestResilience_DebeziumDown`

**Description:** Хелперы в `stack_e2e_test.go`: `stopService`/`startService` через
`docker compose -p delivery-e2e` (`os/exec`), ожидание готовности после старта (`notification` —
по обработке события, Debezium — коннектор `RUNNING`). Каждый тест останавливает компонент,
создаёт заказ (`201`), запускает компонент (и в `t.Cleanup` на случай падения), ждёт обработку
и проверяет «ровно один раз»: строка появилась, счётчик по id заказа не вырос за `stableWindow`.

**Acceptance criteria:**
- [ ] Остановка `notification`: событие обработано после старта ровно один раз
- [ ] Остановка Debezium: событие доставлено после старта ровно один раз
- [ ] После любого исхода теста компонент запущен и готов (`t.Cleanup`)

**Verification:**
- [ ] `task e2e-test` на поднятом стеке; затем `docker compose -p delivery-e2e ps` — всё запущено
- [ ] `task -d e2e lint`

**Dependencies:** T5

**Files likely touched:**
- `e2e/resilience_e2e_test.go`
- `e2e/stack_e2e_test.go`

**Estimated scope:** M

## Task 9: `TestResilience_Redelivery`

**Description:** Создать заказ, дождаться обработки. Остановить `notification`, дождаться пустой
группы, `rpk group seek notification --to start` через `docker compose exec redpanda`, запустить
`notification`. Дождаться, что группа дочитала топик (lag 0 по `rpk group describe`), затем
проверить: для заказа одна строка в `inbox_events` и одна в `notifications` после `stableWindow`.

**Acceptance criteria:**
- [ ] Сдвиг оффсета реально выполнен (lag после seek > 0, затем 0)
- [ ] Дублей по заказу нет ни в `inbox_events`, ни в `notifications`
- [ ] `notification` запущен после теста при любом исходе

**Verification:**
- [ ] `task e2e-test` на поднятом стеке
- [ ] `task -d e2e lint`

**Dependencies:** T8

**Files likely touched:**
- `e2e/resilience_e2e_test.go`
- `e2e/stack_e2e_test.go`

**Estimated scope:** S

## Task 10: Флейк-проверка и итоги модуля

**Description:** Прогоны на долгоживущем стеке и от чистого состояния, замер времени `task e2e`.
Итоги (время прогона, найденные баги, изменения боевого кода) — раздел «Итоги модуля `e2e`» в
карте эпика. После этого — завершение по `spec-workflow` (статус `done`, публикация, архив) —
по команде пользователя.

**Acceptance criteria:**
- [ ] `task e2e-test` с `-count=3` на поднятом стеке без падений, дважды подряд без `e2e-down`
- [ ] `task e2e` дважды подряд зелёный; `task test`, `task test-integration`, `task lint`, `task cover` зелёные
- [ ] Итоги записаны в `spec/specs/testing/map.md`

**Verification:**
- [ ] `task e2e-up && (cd e2e && go test -race -shuffle=on -count=3 -tags=e2e ./...)` ×2
- [ ] `time task e2e` ×2
- [ ] `task test && task test-integration && task lint && task cover`

**Dependencies:** T6, T7, T9

**Files likely touched:**
- `spec/specs/testing/map.md`

**Estimated scope:** S

## Checkpoint: Готово
- [ ] Все Success Criteria спека выполнены
- [ ] Ревью с человеком перед архивированием
