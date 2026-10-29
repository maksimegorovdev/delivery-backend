# Tasks: test-foundation

> План: [plan.md](plan.md) · Спек: [SPEC-test-foundation.md](../spec/testing/SPEC-test-foundation.md)

Для интеграционных задач нужен запущенный Docker. Образы: `postgres:18`, `redpandadata/redpanda:v26.2.3`.

## Task 1: Пакет `platform/testkit/pgtest` со smoke-тестом

**Description:** Создать пакет `github.com/maksimegorovdev/delivery-backend/platform/testkit` внутри модуля `platform`.
Реализовать `pgtest`: общий контейнер `postgres:18` на тестовый бинарь (`sync.Once`), применение миграций
`golang-migrate` из переданного каталога в БД `template_app`, `NewDB(t, migrationsDir) *pgxpool.Pool`
(клон `CREATE DATABASE t_<random> TEMPLATE template_app`, в `t.Cleanup` закрыть пул и `DROP DATABASE`).
Без Docker — понятная ошибка через `t.Fatal`. В модуль добавить `Taskfile.yml` и `.golangci.yml` по образцу `platform`.

**Acceptance criteria:**
- [x] Smoke-тест (`//go:build integration`) на тестовых миграциях из `testdata/` получает БД с таблицей из миграции
- [x] 10 параллельных `NewDB` получают разные БД: запись в одной не видна в другой
- [x] После прогона `docker ps -a` не показывает контейнеров testcontainers, `pgtest` не импортирует другие пакеты `platform`

**Verification:**
- [x] `cd platform && go test -race -count=1 -tags=integration ./testkit/...`
- [x] `docker ps -a --filter label=org.testcontainers=true` → пусто через ~10 с после прогона
- [x] `go list -deps ./testkit/... | grep delivery-backend/platform | grep -v /testkit` → пусто
- [x] `task -d platform lint` зелёный (и `golangci-lint run --build-tags=integration ./testkit/...`)

**Dependencies:** None

**Files likely touched:**
- `platform/go.mod`, `platform/go.sum`
- `platform/testkit/pgtest/pgtest.go`
- `platform/testkit/pgtest/pgtest_integration_test.go`, `platform/testkit/pgtest/testdata/*.sql`

**Estimated scope:** M

## Task 2: `kafkatest` со smoke-тестом

**Description:** `kafkatest`: общий контейнер `redpandadata/redpanda:v26.2.3` (модуль testcontainers `redpanda`)
на тестовый бинарь, `Brokers(t) []string`, `CreateTopic(t, name)` с уникальным суффиксом для изоляции тестов.
Клиент для smoke-теста — `franz-go`, как в `platform/kafka`.

**Acceptance criteria:**
- [ ] Smoke-тест создаёт топик, публикует и читает сообщение
- [ ] Два параллельных теста получают разные топики

**Verification:**
- [ ] `cd platform && go test -race -count=1 -tags=integration ./testkit/kafkatest/...`
- [ ] `task -d platform lint` зелёный (и с `--build-tags=integration`)

**Dependencies:** Task 1

**Files likely touched:**
- `platform/testkit/kafkatest/kafkatest.go`
- `platform/testkit/kafkatest/kafkatest_integration_test.go`
- `platform/go.mod`

**Estimated scope:** S

## Checkpoint: testkit
- [ ] `go test -tags=integration ./...` в `platform/testkit` зелёный, контейнеры удаляются
- [ ] Показать пользователю API `pgtest`/`kafkatest` перед использованием в сервисах

## Task 3: Задачи Taskfile `test` и `test-integration` во всех модулях

**Description:** В Taskfile каждого модуля (`platform`, `proto`, 5 сервисов) задачи
`test` (`go test -race -shuffle=on -count=1 ./...`) и `test-integration` (то же с `-tags=integration`).
В корневом Taskfile — циклы по `MODULES`. Задачу `test` в `platform` привести к общему виду.
Правки механические, поэтому больше 5 файлов допустимо.

**Acceptance criteria:**
- [ ] `task test` из корня проходит по всем модулям и зелёный при остановленном Docker
- [ ] `task test-integration` из корня запускает smoke-тесты `testkit`

**Verification:**
- [ ] `task test` (Docker остановлен) → exit 0
- [ ] `task test-integration` → exit 0
- [ ] `task --list` показывает новые задачи с `desc`

**Dependencies:** Task 1

**Files likely touched:**
- `Taskfile.yml`
- `platform/Taskfile.yml`, `proto/Taskfile.yml`, `services/*/Taskfile.yml`

**Estimated scope:** S (механика, 8 файлов)

## Task 4: mockery v3: установка, `.mockery.yml`, `task mocks`, моки `order/internal/service`

**Description:** Добавить `MOCKERY_VERSION` и установку mockery в `tools` Taskfile `order`. Задача `mocks`
в модуле и корневой цикл по модулям, где есть `.mockery.yml`. Конфиг `services/order/.mockery.yml` из спека
(`all: true` для `internal/service`). Сгенерировать моки и закоммитить в рабочее дерево (коммит — по команде пользователя).
Перед этим сверить ключи конфига с документацией mockery v3.

**Acceptance criteria:**
- [ ] `services/order/internal/service/mocks/mocks.go` содержит `MockTxManager`, `MockOrderRepo`, `MockOutboxRepo`, `MockOrderEvents`, `MockUserProvider`, `MockProductProvider` с `EXPECT()` и `NewMock…(t)`
- [ ] Повторный `task mocks` не меняет файлы
- [ ] Файл помечен `// Code generated`, `golangci-lint` его не проверяет

**Verification:**
- [ ] `task mocks && git diff --exit-code services/order/internal/service/mocks`
- [ ] `go build ./...` и `task -d services/order lint` зелёные

**Dependencies:** Task 3

**Files likely touched:**
- `services/order/Taskfile.yml`, `Taskfile.yml`
- `services/order/.mockery.yml`
- `services/order/internal/service/mocks/mocks.go`
- `services/order/go.mod`

**Estimated scope:** S

## Task 5: Пилотный unit-тест `OrderService.CreateOrder`

**Description:** `services/order/internal/service/order_test.go`, `package service_test`. Табличный тест с моками
mockery: хелпер `newDeps(t)` собирает моки и сервис. `TxManager` — ручной фейк, который вызывает `fn(ctx)`.
Имена кейсов на английском, стиль — как в разделе Code Style спека.

**Acceptance criteria:**
- [ ] Кейсы: ошибка `GetUser` пробрасывается (`ErrorIs`) и `Create` не вызывается; отсутствующий продукт → `ErrorIs(domain.ErrProductNotFound)`; успех — заказ с позициями и ценами из каталога, `Create` и `Save` вызваны внутри tx
- [ ] Тест и подтесты с `t.Parallel()`, без Docker, стабилен под `-shuffle=on -count=20`

**Verification:**
- [ ] `task -d services/order test`
- [ ] `cd services/order && go test -race -shuffle=on -count=20 ./internal/service/...`
- [ ] `task -d services/order lint` зелёный

**Dependencies:** Task 4

**Files likely touched:**
- `services/order/internal/service/order_test.go`
- `services/order/go.mod`

**Estimated scope:** S

## Task 6: Пилотный integration-тест `OrderRepo.Create`

**Description:** `services/order/internal/repository/pg/order_integration_test.go` (`//go:build integration`,
`package pg_test`). `pgtest.NewDB(t, "../../../migrations/pg")`, `pg.NewOrderRepo(pool).Create`, проверка
строк `orders` и позиций прямым SQL. Второй тест параллельно проверяет изоляцию: в его БД записей нет.

**Acceptance criteria:**
- [ ] Заказ и позиции сохранены с корректными полями
- [ ] Параллельный тест не видит данных первого
- [ ] Под `task test` (без тега) тест не компилируется и не запускается

**Verification:**
- [ ] `task -d services/order test-integration`
- [ ] `task -d services/order test` с остановленным Docker → зелёный
- [ ] `task -d services/order lint` зелёный (с тегом: `golangci-lint run --build-tags=integration`)

**Dependencies:** Task 1, Task 3

**Files likely touched:**
- `services/order/internal/repository/pg/order_integration_test.go`
- `services/order/go.mod`

**Estimated scope:** S

## Checkpoint: Пилот
- [ ] `task test` (без Docker) и `task test-integration` зелёные из корня
- [ ] Ревью пилотных тестов с пользователем

## Task 7: Линтеры `thelper`, `testifylint`, `depguard`

**Description:** Во всех `.golangci.yml`: включить `thelper`, `testifylint: enable-all: true`, правило `depguard`
с `files: ["!$test"]`, запрещающее `.../testkit` и `.../mocks`. Задача `lint` должна линтить и
интеграционные файлы (`--build-tags=integration` или `run.build-tags`). Посчитать нарушения в существующих тестах.
Если их больше ~20, остановиться и согласовать перенос в модуль `unit`.

**Acceptance criteria:**
- [ ] `task lint` зелёный во всех модулях
- [ ] Импорт `testkit` в не-тестовом файле даёт ошибку `depguard` (проверено временной правкой)
- [ ] Интеграционные тесты попадают в линт

**Verification:**
- [ ] `task lint`
- [ ] Временный импорт `pgtest` в `services/order/internal/app/app.go` → `depguard` ругается, правка откатывается

**Dependencies:** Task 5, Task 6

**Files likely touched:**
- `platform/.golangci.yml`, `proto/.golangci.yml`, `services/*/.golangci.yml`

**Estimated scope:** S (механика, 7 файлов)

## Task 8: `task cover` + `.testcoverage.yml`

**Description:** Установка `go-test-coverage` (`GO_TEST_COVERAGE_VERSION`) в корневой `tools`. Задача `cover`:
по каждому модулю `go test -race -count=1 -tags=integration -coverprofile=../coverage/<module>.out ./...`,
затем `go-test-coverage --config=.testcoverage.yml`. Конфиг: total 70, override 80 для `/domain$` и `/service$`,
исключения `proto/gen`, `/mocks/`, `/cmd/`, `/docs/`. `coverage/` в `.gitignore`. Синтаксис сверить с документацией.

**Acceptance criteria:**
- [ ] `task cover` печатает покрытие по пакетам всех модулей
- [ ] Исключённые пути в отчёт не входят
- [ ] Нарушение порога даёт ненулевой код. Текущий результат (ожидаемо красный) записан в todo

**Verification:**
- [ ] `task cover; echo $?`
- [ ] Отчёт не содержит `mocks`, `proto/gen`, `cmd`

**Dependencies:** Task 3, Task 5, Task 6

**Files likely touched:**
- `Taskfile.yml`
- `.testcoverage.yml`
- `.gitignore`

**Estimated scope:** S

## Task 9: Раздел «Тесты» в `AGENTS.md`

**Description:** Кратко записать конвенции из спека: команды, build-тег `integration`, `testkit`, mockery и коммит моков,
`require`/`assert`, `t.Parallel`, английские `name`, именование файлов. Ссылка на спек — для деталей.

**Acceptance criteria:**
- [ ] Раздел до ~25 строк, без дублирования спека целиком
- [ ] Команды в разделе реально работают

**Verification:**
- [ ] Ручная: прогнать каждую команду из раздела

**Dependencies:** Task 7, Task 8

**Files likely touched:**
- `AGENTS.md`

**Estimated scope:** XS

## Checkpoint: Complete
- [ ] Все Success Criteria [спека](../spec/testing/SPEC-test-foundation.md) выполнены
- [ ] `task lint`, `task test`, `task test-integration` зелёные; `task cover` печатает отчёт
- [ ] Резюме изменений пользователю, коммит — по команде
