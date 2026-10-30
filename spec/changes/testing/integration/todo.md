# Tasks: integration

> План: [plan.md](plan.md) · Спек: [spec.md](spec.md)

Общее для каждой задачи (Definition of Done, в задачах не повторяется):
- `//go:build integration` на каждом файле, который поднимает контейнер или слушает порт; контейнеры только через `platform/testkit`;
- testify, английские `name`, `t.Parallel` везде, кроме тестов с `t.Setenv`; асинхронность только через `require.EventuallyWithT` (константы в `helpers_test.go` пакета), без `time.Sleep`;
- только свои фикстуры с фиксированными UUID, на seed-миграции не опираться;
- найденный баг: сначала красный тест, затем исправление; изменение контракта — только после согласия; баги перечислены в резюме задачи;
- `task -d <module> test`, `task -d <module> test-integration`, `task -d <module> lint` зелёные; `git commit` — только по команде.

---

## Phase 1: testkit и kafkaconsumer

## Task 1: `pgtest.CheckMigrations` с собственным тестом

**Description:** `CheckMigrations(tb, migrationsDir)` в `platform/testkit/pgtest`: в общем контейнере
создаёт пустую БД без `TEMPLATE`, применяет миграции up → down до нуля → up, после down проверяет, что
в схеме `public` нет ничего, кроме `schema_migrations`. БД удаляется в `tb.Cleanup`, шаблон не трогается,
правило «один каталог миграций на бинарь» общее с `NewDB`. Собственный тест: валидные миграции из
`testdata` проходят; набор с неполным `down` ловится.

**Acceptance criteria:**
- [x] Валидный каталог проходит полный цикл, БД удаляется после теста
- [x] Неполный `down` (таблица остаётся) приводит к падению — проверено тестом на внутренней функции, возвращающей ошибку
- [x] `NewDB` в том же бинаре продолжает работать (шаблон не изменён)

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=1 -tags=integration ./testkit/pgtest/...`
- [x] `task -d platform lint`

**Dependencies:** None

**Files likely touched:**
- `platform/testkit/pgtest/migrations.go`
- `platform/testkit/pgtest/migrations_internal_integration_test.go`
- `platform/testkit/pgtest/testdata/` (второй набор миграций с неполным `down`)

**Estimated scope:** M

## Task 2: `kafkatest.Produce` с собственным тестом

**Description:** `Produce(tb, topic, records...)` — синхронная публикация (`ProduceSync`) с проставлением
`Topic`, падение при любой ошибке брокера. Тест: опубликованные записи (ключ, заголовки, значение)
читаются отдельным клиентом в том же порядке.

**Acceptance criteria:**
- [x] Записи доходят до топика с ключом, заголовками и значением
- [x] Порядок записей в партиции сохранён
- [x] Клиент закрывается внутри `Produce`, утечек нет

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=1 -tags=integration ./testkit/kafkatest/...`
- [x] `task -d platform lint`

**Dependencies:** None

**Files likely touched:**
- `platform/testkit/kafkatest/kafkatest.go`
- `platform/testkit/kafkatest/kafkatest_integration_test.go`

**Estimated scope:** S

## Task 3: Интеграционные тесты `kafkaconsumer` на Redpanda

**Description:** Цикл `Run` на настоящем брокере: уникальные топик и группа на тест, хендлер-фейк
с каналом. Успешные записи коммитятся — новый consumer в той же группе их не получает. Ошибка хендлера
откатывает оффсет: запись приходит снова после `retryBackoff` (малый через `WithRetryBackoff`), следующие
записи партиции не обрабатываются раньше неё. Отмена ctx завершает `Run`, `Close` без утечек.

**Acceptance criteria:**
- [x] Коммит: повторный consumer группы не получает обработанные записи
- [x] Откат и ретрай: упавшая запись обрабатывается повторно и строго до следующей
- [x] Отмена ctx завершает `Run`; `goleak` в `TestMain` зелёный

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=5 -tags=integration ./kafka/kafkaconsumer/...` (стабильность)
- [x] `task -d platform lint`

**Dependencies:** Task 2

**Files likely touched:**
- `platform/kafka/kafkaconsumer/kafkaconsumer_integration_test.go`
- `platform/kafka/kafkaconsumer/main_test.go` (при необходимости опций `goleak`)

**Estimated scope:** S

## Checkpoint 1: инфраструктура
- [x] `task -d platform test-integration` и `task -d platform lint` зелёные
- [x] `task -d platform test` работает без Docker
- [x] Обзор API `CheckMigrations`/`Produce` и образца асинхронной проверки с пользователем

---

## Phase 2: order — эталонный сервис и контракт

## Task 4: `order` — репозитории и миграции

**Description:** Дополнить тест `OrderRepo.Create`: нарушение `CHECK` (quantity ≤ 0) и дубль `id` → коды
`apperr` из `pgerr`. Тест `OutboxRepo.Save` на реальной схеме: колонки строки, `traceparent`/`tracestate`
при активном span и без него (NULL). `migrations_integration_test.go` с `pgtest.CheckMigrations`.
Общие хелперы пакета — в `helpers_integration_test.go`.

**Acceptance criteria:**
- [x] `Create`: нарушение `CHECK` и дубль `id` дают ожидаемые коды `apperr`
- [x] `Save`: все колонки записаны; trace-контекст записан при span и пуст без него
- [x] Миграции `order` проходят up → down → up

**Verification:**
- [x] `task -d services/order test-integration`
- [x] `task -d services/order lint`

**Dependencies:** Task 1

**Files likely touched:**
- `services/order/internal/repository/pg/order_integration_test.go`
- `services/order/internal/repository/pg/outbox_integration_test.go`
- `services/order/internal/repository/pg/migrations_integration_test.go`
- `services/order/internal/repository/pg/helpers_integration_test.go`

**Estimated scope:** M

## Task 5: `order` — атомарность заказа и outbox

**Description:** Реальный `postgres.TxManager` + `OrderRepo.Create` + `OutboxRepo.Save` в одной транзакции.
Ошибка `Save` (например, дубль `id` события или ошибка, вызванная в колбэке после `Create`) откатывает
транзакцию. Плюс положительный кейс: заказ, позиции и событие видны после коммита.

**Acceptance criteria:**
- [x] Ошибка `Save` не оставляет строк в `orders`, `order_items`, `outbox_events`
- [x] Успешная транзакция фиксирует все три таблицы

**Verification:**
- [x] `task -d services/order test-integration`

**Dependencies:** Task 4

**Files likely touched:**
- `services/order/internal/repository/pg/tx_integration_test.go`

**Estimated scope:** S

## Task 6: Эталон `order.created` и тест продюсера

**Description:** `TestOrderCreated_Golden`: фиксированный заказ → `event.OrderEventBuilder` → `OutboxRepo.Save`
→ чтение строки → сообщение по правилам `EventRouter` из `deploy/debezium/order-outbox.json`
(`aggregate_id` → `key`, `type` → `headers.event-type`, `payload` → `value`). `event_id` подставляется
из эталона, формат проверяется отдельно (`uuid.Parse`, версия 7). Сравнение `require.JSONEq` с
`contracts/events/order.created.json`; флаг `-update` перезаписывает файл. Константа `contractsDir`
в `helpers_integration_test.go`.

**Acceptance criteria:**
- [x] `contracts/events/order.created.json` создан через `-update`, содержимое приведено в резюме
- [x] Без `-update` тест сравнивает и проходит; ручная правка любого поля эталона роняет тест
- [x] `event_id` проверен как UUIDv7, `traceparent`/`tracestate` в эталон не входят

**Verification:**
- [x] `cd services/order && go test -tags=integration -run TestOrderCreated_Golden ./internal/repository/pg -update`, затем без `-update`
- [x] Ручная проверка: изменить поле в эталоне → тест красный → вернуть
- [x] `task -d services/order lint`

**Dependencies:** Task 4

**Files likely touched:**
- `contracts/events/order.created.json`
- `services/order/internal/repository/pg/contract_integration_test.go`
- `services/order/internal/repository/pg/helpers_integration_test.go`

**Estimated scope:** M

## Task 7: `order` — компонентный gRPC-стек

**Description:** `grpctest.NewConn` с интерцепторами из `app` (`Error`, `Logger`, `Validation`) → реальный
`OrderService` → репозитории на `pgtest` и реальный `TxManager`; `user`/`product` — фейковые gRPC-серверы
(`grpctest`). `CreateOrder` пишет заказ, позиции и outbox-событие; ошибки зависимостей (не найдено)
и валидации дают ожидаемые коды gRPC.

**Acceptance criteria:**
- [x] `CreateOrder` возвращает заказ, в БД есть заказ, позиции и событие `order.created`
- [x] «Не найдено» от `user`/`product` → соответствующий код gRPC, в БД ничего не записано
- [x] Невалидный запрос отсекается интерцептором валидации

**Verification:**
- [x] `task -d services/order test-integration`
- [x] `task -d services/order lint`

**Dependencies:** Task 4

**Files likely touched:**
- `services/order/internal/transport/grpc/order_integration_test.go`
- `services/order/internal/transport/grpc/helpers_integration_test.go`

**Estimated scope:** M

## Checkpoint 2: order
- [x] `task -d services/order test-integration` и `task -d services/order lint` зелёные
- [x] Эталон `contracts/events/order.created.json` согласован с пользователем

---

## Phase 3: notification

## Task 8: `notification` — репозитории и миграции

**Description:** `InboxRepo.Claim`: запись полей; повтор `event_id` не падает и не дублирует строку.
`NotificationRepo.CreateForChannels`: строка на канал; повтор `(event_id, channel)` без дубля; пустой
список каналов — без запросов; недопустимый канал → ошибка `apperr`. `CheckMigrations` для миграций сервиса.

**Acceptance criteria:**
- [x] `Claim` идемпотентен по `event_id` и возвращает признак повтора согласно контракту метода
- [x] `CreateForChannels` покрывает все четыре кейса спека
- [x] Миграции `notification` проходят up → down → up

**Verification:**
- [x] `task -d services/notification test-integration`
- [x] `task -d services/notification lint`

**Dependencies:** Task 1

**Files likely touched:**
- `services/notification/internal/repository/pg/inbox_integration_test.go`
- `services/notification/internal/repository/pg/notification_integration_test.go`
- `services/notification/internal/repository/pg/migrations_integration_test.go`
- `services/notification/internal/repository/pg/helpers_integration_test.go`

**Estimated scope:** M

## Task 9: `notification` — компонентный Kafka-стек на эталоне

**Description:** `runConsumer` поднимает реальные repo → service → router/handler и `kafkaconsumer`
(уникальные топик и группа, `Run` в горутине, остановка в `t.Cleanup`). `goldenRecord` читает
`contracts/events/order.created.json` в `*kgo.Record`. Кейсы: событие обработано, `payload` в
`notifications` совпадает с `value` эталона (`JSONEq`); повторная доставка — без дублей; неизвестный тип
события коммитится, следующее сообщение обрабатывается; невалидное сообщение не коммитится и ретраится
(фиксация текущего поведения).

**Acceptance criteria:**
- [x] Эталон обрабатывается, строки в `inbox_events`/`notifications` по одной на событие/канал
- [x] Повторная доставка того же события не создаёт дублей
- [x] Неизвестный тип не блокирует партицию; невалидное сообщение ретраится (текущее поведение зафиксировано)

**Verification:**
- [x] `cd services/notification && go test -race -shuffle=on -count=5 -tags=integration ./internal/transport/kafka/...` (стабильность)
- [x] `task -d services/notification lint`

**Dependencies:** Task 2, Task 3, Task 6, Task 8

**Files likely touched:**
- `services/notification/internal/transport/kafka/consumer_integration_test.go`
- `services/notification/internal/transport/kafka/helpers_integration_test.go`
- `services/notification/internal/transport/kafka/helpers_test.go` (константы `eventually*`)

**Estimated scope:** M

---

## Phase 4: user и product

## Task 10: `user` — репозитории, миграции, компонентный gRPC-стек

**Description:** `UserRepo.GetByID`, `AddressRepo.GetByID` на своих фикстурах: найдено, не найдено →
`NotFound`, не-UUID → ошибка `apperr`. `CheckMigrations`. Компонентный стек по образцу T7 (без внешних
зависимостей): запрос → ответ, «не найдено» → код gRPC.

**Acceptance criteria:**
- [x] Оба репозитория покрыты тремя кейсами спека
- [x] Миграции `user` проходят up → down → up
- [x] gRPC-стек: найдено / не найдено / невалидный id дают ожидаемые ответы и коды

**Verification:**
- [x] `task -d services/user test-integration`
- [x] `task -d services/user lint`

**Dependencies:** Task 1, Task 7 (образец)

**Files likely touched:**
- `services/user/internal/repository/pg/user_integration_test.go`
- `services/user/internal/repository/pg/address_integration_test.go`
- `services/user/internal/repository/pg/migrations_integration_test.go`
- `services/user/internal/transport/grpc/user_integration_test.go`
- `services/user/internal/transport/grpc/helpers_integration_test.go`

**Estimated scope:** M

## Task 11: `product` — репозиторий, миграции, компонентный gRPC-стек

**Description:** `ProductRepo.GetByIDs`: все найдены, часть отсутствует, пустой список, не-UUID → ошибка
`apperr`. `CheckMigrations`. Компонентный gRPC-стек по образцу T10.

**Acceptance criteria:**
- [x] `GetByIDs` покрыт четырьмя кейсами спека
- [x] Миграции `product` проходят up → down → up
- [x] gRPC-стек: ответ и коды ошибок соответствуют поведению сервиса

**Verification:**
- [x] `task -d services/product test-integration`
- [x] `task -d services/product lint`

**Dependencies:** Task 1, Task 7 (образец)

**Files likely touched:**
- `services/product/internal/repository/pg/product_integration_test.go`
- `services/product/internal/repository/pg/migrations_integration_test.go`
- `services/product/internal/transport/grpc/product_integration_test.go`
- `services/product/internal/transport/grpc/helpers_integration_test.go`

**Estimated scope:** M

## Checkpoint 3: все репозитории и компонентные стеки
- [x] `task test-integration` зелёный во всех модулях, `task test` работает без Docker
- [x] `task lint` зелёный
- [ ] Ревью найденных багов с пользователем

---

## Phase 5: шов App.Addr() и smoke app

## Task 12: Шов `App.Addr()` и smoke-тест `order`

**Description:** В `order/internal/app`: `New` открывает листенер (`net.Listen` на адрес из конфига)
и закрывает его при ошибке `New`; `Run` вызывает `grpcServer.Serve(ctx, lis)`; `Addr() net.Addr`.
Повторное закрытие листенера (его уже закрыл `Serve`) не даёт ошибку в `Close`. Smoke-тест
(`//nolint:paralleltest // t.Setenv`): env с `127.0.0.1:0`, DSN из `pgtest`, телеметрия выключена,
`user`/`product` — фейковые gRPC-серверы на loopback-листенере; `Run` в горутине → `CreateOrder` на
`Addr()` → отмена ctx → `Run` вернул nil → `Close` без ошибок.

**Acceptance criteria:**
- [x] `Addr()` возвращает фактический порт, тест не ищет свободный порт заранее
- [x] Занятый порт → ошибка из `New`, ресурсы освобождены
- [x] Smoke: запрос обслужен, `Run` и `Close` завершаются без ошибок

**Verification:**
- [x] `task -d services/order test-integration`
- [x] `task -d services/order test` (без Docker) и `task -d services/order lint`
- [x] Ручная проверка: `docker compose up` + `order` стартует и логирует фактический адрес

**Dependencies:** Task 7

**Files likely touched:**
- `services/order/internal/app/app.go`
- `services/order/internal/app/app_integration_test.go`

**Estimated scope:** S

## Task 13: Шов и smoke-тесты `user` и `product`

**Description:** Повторить T12 в `user` и `product`; smoke — `GetUser`/`GetProduct` (или ближайший
`GetX`) по своей фикстуре на `Addr()`.

**Acceptance criteria:**
- [x] Оба сервиса отдают `Addr()`, `New` падает на занятом порту
- [x] Smoke-тест каждого сервиса обслуживает один запрос и корректно закрывается

**Verification:**
- [x] `task -d services/user test-integration` и `task -d services/product test-integration`
- [x] `task -d services/user lint` и `task -d services/product lint`

**Dependencies:** Task 12

**Files likely touched:**
- `services/user/internal/app/app.go`
- `services/user/internal/app/app_integration_test.go`
- `services/product/internal/app/app.go`
- `services/product/internal/app/app_integration_test.go`

**Estimated scope:** M

## Task 14: Шов и smoke-тест `gateway`

**Description:** Шов для HTTP: листенер в `New`, `httpServer.Serve(ctx, lis)` в `Run`, `Addr()`.
Smoke: фейковый `order` gRPC-сервер на loopback, HTTP `GET` через gateway на `Addr()` → ожидаемый
статус и тело → отмена ctx → `Close` без ошибок.

**Acceptance criteria:**
- [x] `gateway` отдаёт `Addr()`, `New` падает на занятом порту
- [x] HTTP-запрос проходит gateway → фейковый `order` и возвращает ожидаемый ответ
- [x] `Run`/`Close` без ошибок

**Verification:**
- [x] `task -d services/gateway test-integration`
- [x] `task -d services/gateway lint`

**Dependencies:** Task 12

**Files likely touched:**
- `services/gateway/internal/app/app.go`
- `services/gateway/internal/app/app_integration_test.go`

**Estimated scope:** S

## Task 15: Smoke-тест `notification`

**Description:** Боевой код не меняется. Env: брокер из `kafkatest`, уникальные топик и группа, DSN из
`pgtest`. `Run` в горутине → `kafkatest.Produce` эталона → строка в `notifications` (`EventuallyWithT`)
→ отмена ctx → `Close` без ошибок.

**Acceptance criteria:**
- [x] Одно сообщение обработано реальным `app`
- [x] `Run` завершается по ctx, `Close` без ошибок

**Verification:**
- [x] `task -d services/notification test-integration`
- [x] `task -d services/notification lint`

**Dependencies:** Task 9

**Files likely touched:**
- `services/notification/internal/app/app_integration_test.go`

**Estimated scope:** S

## Task 16: `task cover` зелёный, итоги в карте эпика

**Description:** Прогнать `task cover`, добрать пробелы до порогов (`domain`/`service` ≥ 80%, каждый пакет
`platform` ≥ 80%, `total` ≥ 70%) без изменения `.testcoverage.yml`. Записать в
`spec/specs/testing/map.md` раздел «Итоги модуля `integration`»: покрытие `repository/pg` и `app`
каждого сервиса, `total` по сервисам, найденные баги, изменения боевого кода.

**Acceptance criteria:**
- [ ] `task cover` зелёный во всех модулях
- [ ] Итоги модуля записаны в карту эпика
- [ ] Все Success Criteria спека отмечены

**Verification:**
- [ ] `task cover`, `task test`, `task test-integration`, `task lint`

**Dependencies:** Task 1–15

**Files likely touched:**
- `spec/specs/testing/map.md`
- тесты по отчёту покрытия

**Estimated scope:** S–M

## Checkpoint: Complete
- [ ] Все Success Criteria спека выполнены
- [ ] Резюме модуля готово, ревью с пользователем; затем архивирование по `spec-workflow`
