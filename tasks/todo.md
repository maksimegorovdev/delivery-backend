# Tasks: unit

> План: [plan.md](plan.md) · Спек: [SPEC-unit.md](../spec/testing/SPEC-unit.md)

Общее для каждой задачи (Definition of Done, в задачах не повторяется):
- тесты в `package x_test`, testify, английские `name`, `t.Parallel`, без `time.Sleep`;
- моки только через `task mocks`, повторный запуск без diff;
- найденный баг: сначала красный тест, затем исправление; изменение контракта — только после согласия; исправленные баги перечислены в резюме задачи;
- `task -d <module> test` и `task -d <module> lint` зелёные; `git commit` — только по команде.

---

## Phase 1: platform — фундамент и чистые функции

## Task 1: `platform/testkit/grpctest` с собственным тестом

**Description:** Хелпер `NewConn(t testing.TB, register func(*grpc.Server), opts ...grpc.ServerOption) *grpc.ClientConn`:
in-memory gRPC-сервер на `bufconn`, клиент через `grpc.NewClient` с bufconn-диалером, закрытие сервера
и соединения в `t.Cleanup`. Пакет не импортирует другие пакеты `platform`. Собственный тест на
встроенном health-сервисе (`grpc/health`) — вызов доходит до сервера; `goleak` после `Cleanup`.

**Acceptance criteria:**
- [x] Вызов через возвращённый `conn` доходит до сервера, зарегистрированного в `register`
- [x] `opts` применяются к серверу (тест с интерцептором-счётчиком)
- [x] После завершения теста горутины не утекают (`goleak.VerifyTestMain`), тега `integration` нет

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=10 ./testkit/grpctest/...`
- [x] `task -d platform lint` (`depguard`: `testkit` не импортируется из не-тестовых файлов)

**Dependencies:** None

**Files likely touched:**
- `platform/testkit/grpctest/grpctest.go`
- `platform/testkit/grpctest/grpctest_test.go`
- `platform/testkit/grpctest/main_test.go`

**Estimated scope:** S

## Task 2: Тесты `platform/apperr`

**Description:** Таблицы для конструкторов (`constructors.go`), `Error` (`Wrap`, `Error()`, `Unwrap`,
`errors.Is`/`errors.As`), кодов (`code.go`: строковое представление, HTTP- и gRPC-соответствия) и `reason.go`.

**Acceptance criteria:**
- [x] Каждый конструктор проверен на код и сообщение
- [x] `Wrap` сохраняет причину: `errors.Is`/`errors.As` находят и `*apperr.Error`, и исходную ошибку
- [x] Покрытие пакета ≥ 80%

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=1 -cover ./apperr/`

**Dependencies:** None

**Files likely touched:**
- `platform/apperr/error_test.go`
- `platform/apperr/code_test.go`
- `platform/apperr/constructors_test.go`

**Estimated scope:** S

## Task 3: Тесты `apperr/grpcerr`, `apperr/ozzoerr`, `apperr/pgerr`

**Description:** `grpcerr`: маппинг `apperr` → `status` и обратно (`Map`), для каждого кода, плюс
не-gRPC ошибка. `ozzoerr`: `validation.Errors` и одиночная ошибка ozzo → `apperr` с деталями полей, `nil` → `nil`.
`pgerr`: `pgconn.PgError` с кодами (unique, FK, not null и т. д.) и `pgx.ErrNoRows` → коды `apperr`.

**Acceptance criteria:**
- [x] Маппинг кодов gRPC проверен в обе стороны для всех кодов `apperr`
- [x] Ошибки валидации ozzo дают `InvalidArgument` с перечнем полей
- [x] Покрытие каждого пакета ≥ 80%

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=1 -cover ./apperr/...`

**Dependencies:** Task 2

**Files likely touched:**
- `platform/apperr/grpcerr/grpcerr_test.go`
- `platform/apperr/ozzoerr/ozzoerr_test.go`
- `platform/apperr/pgerr/pgerr_test.go`

**Estimated scope:** M

## Task 4: Тесты `appctx`, `closer`, `http/response`, `internal/otelenv`

**Description:** `appctx`: запись и чтение `request_id`, `user_id`, `error_box` из контекста, пустой контекст.
`closer`: порядок закрытия (LIFO или как реализовано), сбор ошибок, повторный вызов. `response`: статус,
`Content-Type`, тело JSON для успеха и для `apperr` (через `httptest.ResponseRecorder`). `otelenv`: таблица по env.

**Acceptance criteria:**
- [x] Порядок закрытия и агрегирование ошибок `closer` зафиксированы тестом
- [x] `response` отдаёт HTTP-код, соответствующий коду `apperr`, и тело ошибки
- [x] Покрытие `appctx` и `http/response` ≥ 80%

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=1 -cover ./appctx/ ./closer/ ./http/response/ ./internal/otelenv/`

**Dependencies:** Task 2

**Files likely touched:**
- `platform/appctx/appctx_test.go`
- `platform/closer/closer_test.go`
- `platform/http/response/response_test.go`
- `platform/internal/otelenv/otelenv_test.go`

**Estimated scope:** M

## Task 5: Тесты и fuzz `http/bind`, задача `task fuzz`

**Description:** Таблица для `bind.JSON`: валидное тело, битый JSON, ошибка `Validate()`. Fuzz-цель `FuzzJSON`:
любой вход не паникует, ошибка — `apperr` с кодом `InvalidArgument`; seed-корпус (валидный пример, пустой
ввод, `null`, обрезанный JSON, лишние поля). Задача `fuzz` в `platform/Taskfile.yml` обходит `FUZZ_TARGETS`
(`./http/bind:FuzzJSON`) с `FUZZTIME` (по умолчанию `30s`); корневая `fuzz` — цикл по `FUZZ_MODULES`.

**Acceptance criteria:**
- [x] `task test` прогоняет seed-корпус `FuzzJSON`
- [x] `task fuzz` и `task -d platform fuzz FUZZTIME=5s` запускают цель и завершаются с кодом 0
- [x] Покрытие `http/bind` ≥ 80%

**Verification:**
- [x] `cd platform && go test -race -count=1 -cover ./http/bind/`
- [x] `task fuzz FUZZTIME=5s`

**Dependencies:** Task 3 (`ozzoerr`)

**Files likely touched:**
- `platform/http/bind/bind_test.go`
- `platform/http/bind/testdata/fuzz/FuzzJSON/` (при находках)
- `platform/Taskfile.yml`
- `Taskfile.yml`

**Estimated scope:** M

## Checkpoint 1: platform-фундамент
- [ ] `task -d platform test` и `task -d platform lint` зелёные
- [ ] `task fuzz FUZZTIME=5s` работает
- [ ] Обзор API `grpctest` и образца табличного теста с пользователем

---

## Phase 2: platform — транспорт и горутины

## Task 7: Тесты `grpc/interceptors` на `grpctest`

**Description:** `error`: ошибка handler-а (`apperr` каждого кода, обычная ошибка, panic, если обрабатывается) →
код и сообщение `status`. `validation`: запрос с невалидным `Validate()` → `InvalidArgument`, валидный проходит.
`logger`: запись лога с методом и кодом (логгер в буфер). Тестовый сервис — health или echo на `Unimplemented*Server`.

**Acceptance criteria:**
- [x] Каждый код `apperr` превращается в ожидаемый код gRPC через реальную цепочку интерцепторов
- [x] Валидация отсекает невалидный запрос до handler-а (handler не вызван)
- [x] Тесты используют `grpctest.NewConn`, своей bufconn-обвязки нет

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=1 -cover ./grpc/interceptors/`

**Dependencies:** Tasks 1, 3

**Files likely touched:**
- `platform/grpc/interceptors/error_test.go`
- `platform/grpc/interceptors/validation_test.go`
- `platform/grpc/interceptors/logger_test.go`
- `platform/grpc/interceptors/helpers_test.go`

**Estimated scope:** M

## Task 8: Тесты `grpcserver` и `grpcclient`, `goleak`

**Description:** `grpcserver`: опции применяются, reflection регистрируется по опции, `Run` завершается по
отмене контекста (graceful) и возвращает ошибку при занятом адресе. `grpcclient`: `New` применяет опции,
ошибка на некорректном адресе. `goleak.VerifyTestMain` в `grpcserver`. `Run` запускается на `127.0.0.1:0`;
занятый адрес — второй сервер на адресе первого.

**Acceptance criteria:**
- [x] Остановка по `ctx` возвращает `nil`, горутины не утекают
- [x] Ошибка `Listen` оборачивается с префиксом `grpcserver:`
- [x] Стабильно под `-count=10`

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=10 ./grpc/grpcserver/ ./grpc/grpcclient/`

**Dependencies:** Task 1

**Files likely touched:**
- `platform/grpc/grpcserver/server_test.go`
- `platform/grpc/grpcserver/main_test.go`
- `platform/grpc/grpcclient/client_test.go`

**Estimated scope:** M

## Task 9: Тесты `http/middleware`

**Description:** Дополнить пакет (сейчас 28%): `fake_auth` (кладёт `user_id` в контекст / отказ без заголовка —
как реализовано), `logger` (поля лога), `recover` (panic → 500 и лог, следующий запрос обслуживается),
`route` (шаблон маршрута в контексте/метриках). Существующий `request_id_test.go` проверить на конвенции.

**Acceptance criteria:**
- [x] Каждый middleware проверен через `httptest` на реальном `http.Handler`
- [x] Panic в handler-е не роняет тест и даёт 500
- [x] Все имена кейсов на английском

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=1 -cover ./http/middleware/`

**Dependencies:** Task 4 (`appctx`, `response`)

**Files likely touched:**
- `platform/http/middleware/fake_auth_test.go`
- `platform/http/middleware/logger_test.go`
- `platform/http/middleware/recover_test.go`
- `platform/http/middleware/route_test.go`

**Estimated scope:** M

## Task 10: Тесты `httpserver` (graceful shutdown), `goleak`

**Description:** Сервер на `127.0.0.1:0`: опции, остановка по контексту, таймаут graceful shutdown, ошибка старта.
`goleak.VerifyTestMain`.
Перевести имена кейсов `otel_test.go` на английский.

**Acceptance criteria:**
- [x] Graceful shutdown дожидается активного запроса (синхронизация каналами)
- [x] Горутины не утекают; стабильно под `-count=10`
- [x] В `otel_test.go` нет кириллицы в `name`/`t.Run`

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=10 ./http/httpserver/`

**Dependencies:** None

**Files likely touched:**
- `platform/http/httpserver/server_test.go`
- `platform/http/httpserver/main_test.go`
- `platform/http/httpserver/otel_test.go`

**Estimated scope:** M

## Task 11: Unit-тесты `kafkaconsumer`

**Description:** Без брокера: обработка одного сообщения, ошибка обработчика (коммит/повтор — как реализовано),
построение `Message` из записи, опции. Перевести `process_test.go` на testify. Цикл чтения — модуль `integration`.

**Acceptance criteria:**
- [x] Поведение при ошибке обработчика зафиксировано тестом
- [x] `process_test.go` на testify, кейсы не ослаблены
- [x] Покрытие пакета выросло относительно 14% (значение — в резюме)

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=10 -cover ./kafka/kafkaconsumer/`

**Dependencies:** None

**Files likely touched:**
- `platform/kafka/kafkaconsumer/process_test.go`
- `platform/kafka/kafkaconsumer/message_test.go`
- `platform/kafka/kafkaconsumer/options_test.go`

**Estimated scope:** S

## Task 6: Порог 80% на каждый пакет `platform`

> Номер сохранён, выполняется после Task 11: решение по каждому пакету принимается, когда у всех пакетов `platform` есть тесты.

**Description:** В `platform/.testcoverage.yml` удалить неработающие правила `/domain$` и `/service$`, задать
`threshold.package: 80`. Прогнать `task -d platform cover` и для каждого пакета ниже 80% решить: дописать тесты
(unit-тест или интеграционный на `pgtest`/`kafkatest`, `task cover` и так идёт с Docker). `testkit/` тоже под порогом:
у него есть собственные тесты. Пороговых override нет: они фиксируют низкое покрытие вместо того, чтобы его устранить.
Недостижимая ветка вне I/O помечается `// coverage-ignore` с причиной в doc-комментарии.

**Acceptance criteria:**
- [x] `task -d platform cover` зелёный: каждый пакет ≥ 80%, override отсутствуют
- [x] `postgres` и `testkit/pgtest` покрыты интеграционными тестами (≥ 80%), `coverage-ignore` только на недостижимой ветке `otelenv.Unset`
- [x] Исключение (`mocks/`) и `coverage-ignore` перечислены в резюме задачи

**Verification:**
- [x] `task -d platform cover` (Docker запущен)
- [x] Временный пустой пакет с одной непокрытой функцией делает `cover` красным (новый пакет под порогом), пакет удалён

**Dependencies:** Tasks 2–5, 7–11

**Files likely touched:**
- `platform/.testcoverage.yml`

**Estimated scope:** XS

## Checkpoint 2: platform готов
- [x] `task -d platform test`, `task -d platform lint` зелёные
- [x] `task -d platform cover` зелёный с новыми порогами

---

## Phase 3: order — эталонный сервис

## Task 12: Тесты `order/internal/domain`

**Description:** Таблица для `NewOrder`: пустые позиции → `ErrOrderNoItems`, расчёт `TotalAmount`, ID позиций
и `OrderID` (формат UUID), статус `created`, время в UTC; `OrderItem.Subtotal`.

**Acceptance criteria:**
- [x] Все ветки `NewOrder` покрыты
- [x] UUID и время проверяются по формату (`uuid.Parse`, `Location() == time.UTC`), не по значению
- [x] Покрытие `domain` ≥ 80%

**Verification:**
- [x] `cd services/order && go test -race -shuffle=on -count=1 -cover ./internal/domain/`

**Dependencies:** None

**Files likely touched:**
- `services/order/internal/domain/order_test.go`

**Estimated scope:** XS

## Task 13: Тесты и fuzz `order/internal/event`

**Description:** `OrderCreated`: payload разбирается `protojson` обратно в `orderv1.OrderCreated` с теми же
полями, имена полей `snake_case`, поля `OutboxEvent` (тип, aggregate, ключ). Fuzz-цель `FuzzOrderCreated`
(round-trip: заказ собирается из примитивов `testing.F` — id, суммы, строки, число позиций). `FUZZ_TARGETS` и задача `fuzz` в Taskfile
`order`, модуль добавлен в `FUZZ_MODULES`.

**Acceptance criteria:**
- [x] Round-trip тест проходит для заказа с несколькими позициями
- [x] `task -d services/order fuzz FUZZTIME=5s` запускает цель
- [x] `task fuzz` из корня запускает цели `platform` и `order`

**Verification:**
- [x] `cd services/order && go test -race -shuffle=on -count=1 -cover ./internal/event/`
- [x] `task fuzz FUZZTIME=5s`

**Dependencies:** Task 5

**Files likely touched:**
- `services/order/internal/event/order_test.go`
- `services/order/Taskfile.yml`
- `Taskfile.yml`

**Estimated scope:** S

## Task 14: Дополнить тесты `order/internal/service`

**Description:** Сейчас 87.9%. Найти непокрытые ветки (`go tool cover -func`) и use case-ы без тестов
(например, `GetOrder`), добавить кейсы: ошибка каждой зависимости, порядок вызовов в транзакции.

**Acceptance criteria:**
- [x] Каждый публичный метод `OrderService` имеет тест успеха и ошибки каждой зависимости
- [x] Покрытие `service` не ниже текущего и ≥ 80%

**Verification:**
- [x] `cd services/order && go test -race -shuffle=on -count=1 -coverprofile=/tmp/order.out ./internal/service/ && go tool cover -func=/tmp/order.out`

**Dependencies:** None

**Files likely touched:**
- `services/order/internal/service/order_test.go`

**Estimated scope:** S

## Task 15: Тесты `order/internal/client` на `grpctest`

**Description:** `UserClient` (`GetUser`, `GetAddress`) и `ProductClient`: фейковые серверы на
`Unimplemented*Server`, клиент на `conn` из `grpctest.NewConn`. Проверить передачу аргументов, маппинг
proto → domain (все поля, время), маппинг кодов gRPC → `apperr` через `grpcerr.Map`.

**Acceptance criteria:**
- [x] Для каждого метода: успех с проверкой всех полей domain и ошибка `NotFound` → `apperr` NotFound
- [x] Фейковый сервер проверяет полученный `Id`
- [x] Своей bufconn-обвязки нет

**Verification:**
- [x] `cd services/order && go test -race -shuffle=on -count=1 -cover ./internal/client/`

**Dependencies:** Tasks 1, 3

**Files likely touched:**
- `services/order/internal/client/user_test.go`
- `services/order/internal/client/product_test.go`
- `services/order/internal/client/helpers_test.go`

**Estimated scope:** M

## Task 16: Тесты `order/internal/transport/grpc` на `grpctest`

**Description:** В `.mockery.yml` добавить пакет `transport/grpc`, сгенерировать `MockOrderService`.
Тесты handler-а: маппинг request → вход use case, результат → response, ошибки `apperr` → коды gRPC через
те же интерцепторы, что в `app` сервиса. Образец — блок кода в спеке.

**Acceptance criteria:**
- [x] Каждый RPC: успех с проверкой ответа и аргументов мока, ошибка сервиса → ожидаемый код
- [x] Цепочка интерцепторов совпадает с `app` (валидация отсекает невалидный запрос до сервиса)
- [x] `task mocks` повторно без diff

**Verification:**
- [x] `task -d services/order mocks && git diff --exit-code services/order`
- [x] `cd services/order && go test -race -shuffle=on -count=1 -cover ./internal/transport/grpc/`

**Dependencies:** Tasks 1, 7

**Files likely touched:**
- `services/order/.mockery.yml`
- `services/order/internal/transport/grpc/mocks/mocks.go`
- `services/order/internal/transport/grpc/order_test.go`

**Estimated scope:** M

## Checkpoint 3: образцы закреплены
- [x] `task -d services/order test`, `lint`, `cover` зелёные
- [x] Обзор образцов тестов клиента (Task 15) и транспорта (Task 16) с пользователем перед копированием

---

## Phase 4: остальные сервисы (задачи 17–23 независимы)

## Task 17: user — моки и тесты `service`

**Description:** Создать `services/user/.mockery.yml` (пакеты `service` и `transport/grpc`), сгенерировать моки.
Тесты `UserService` и `AddressService`: успех и ошибка репозитория для каждого метода.

**Acceptance criteria:**
- [x] Каждый use case: успех, ошибка репозитория пробрасывается
- [x] Покрытие `service` ≥ 80%
- [x] `task mocks` повторно без diff

**Verification:**
- [x] `cd services/user && go test -race -shuffle=on -count=1 -cover ./internal/service/`

**Dependencies:** Checkpoint 3

**Files likely touched:**
- `services/user/.mockery.yml`
- `services/user/internal/service/mocks/mocks.go`
- `services/user/internal/service/user_test.go`
- `services/user/internal/service/address_test.go`

**Estimated scope:** M

## Task 18: user — тесты `transport/grpc`

**Description:** Handler-ы `User` и `Address` на `grpctest` с моками сервисов и интерцепторами как в `app`.

**Acceptance criteria:**
- [x] Каждый RPC: успех с проверкой ответа, ошибка сервиса → код gRPC
- [x] Используется `grpctest.NewConn`

**Verification:**
- [x] `cd services/user && go test -race -shuffle=on -count=1 -cover ./internal/transport/grpc/`

**Dependencies:** Task 17

**Files likely touched:**
- `services/user/internal/transport/grpc/mocks/mocks.go`
- `services/user/internal/transport/grpc/user_test.go`
- `services/user/internal/transport/grpc/address_test.go`

**Estimated scope:** M

## Task 19: product — моки, тесты `service` и `transport/grpc`

**Description:** Сервис маленький (23 + 54 строки). `.mockery.yml` (`service`, `transport/grpc`), тесты
`ProductService` и handler-а на `grpctest`.

**Acceptance criteria:**
- [x] Use case и RPC: успех и ошибка
- [x] Покрытие `service` ≥ 80%
- [x] `task mocks` повторно без diff

**Verification:**
- [x] `cd services/product && go test -race -shuffle=on -count=1 -cover ./internal/...`

**Dependencies:** Checkpoint 3

**Files likely touched:**
- `services/product/.mockery.yml`
- `services/product/internal/service/product_test.go`
- `services/product/internal/transport/grpc/product_test.go`
- моки в `service/mocks/`, `transport/grpc/mocks/`

**Estimated scope:** M

## Task 20: notification — моки и тесты `service`

**Description:** `.mockery.yml` (`service`, `transport/kafka/order`). Тесты `CreateNotification` с ручным
фейком `TxManager`: `Claim` и `CreateForChannels` вызываются внутри транзакции и по порядку; ошибка `Claim`
(в т. ч. «уже обработано» — идемпотентность inbox) → уведомление не создаётся; ошибка репозитория пробрасывается.

**Acceptance criteria:**
- [x] Повторное событие (ошибка/результат `Claim` для дубля) не создаёт уведомление
- [x] Оба вызова сделаны внутри `WithinTx`
- [x] Покрытие `service` ≥ 80%

**Verification:**
- [x] `cd services/notification && go test -race -shuffle=on -count=1 -cover ./internal/service/`

**Dependencies:** Checkpoint 3

**Files likely touched:**
- `services/notification/.mockery.yml`
- `services/notification/internal/service/mocks/mocks.go`
- `services/notification/internal/service/notification_test.go`

**Estimated scope:** S

## Task 21: notification — тесты и fuzz `transport/kafka`

**Description:** `Router`: маршрут по топику, неизвестный топик → `nil`. `order.Handler`: роутинг по заголовку
типа события, неизвестный тип → `nil` и сервис не вызван (tolerant reader), битый payload → `InvalidArgument`
и сервис не вызван, заполнение `InboxEvent` из сообщения. Fuzz `FuzzHandleOrderCreated`; `FUZZ_TARGETS` в
Taskfile `notification`, модуль в `FUZZ_MODULES`.

**Acceptance criteria:**
- [x] Поля `InboxEvent` и `Payload` во входе сервиса совпадают с сообщением
- [x] Любой `msg.Value` не паникует; ошибка — `InvalidArgument`, сервис не вызывается
- [x] `task fuzz` запускает все три цели

**Verification:**
- [x] `cd services/notification && go test -race -shuffle=on -count=1 -cover ./internal/transport/...`
- [x] `task fuzz FUZZTIME=5s`

**Dependencies:** Tasks 5, 20

**Files likely touched:**
- `services/notification/internal/transport/kafka/router_test.go`
- `services/notification/internal/transport/kafka/order/handler_test.go`
- `services/notification/internal/transport/kafka/order/mocks/mocks.go`
- `services/notification/Taskfile.yml`
- `Taskfile.yml`

**Estimated scope:** M

## Task 22: gateway — моки, тесты `service` и `client`

**Description:** `.mockery.yml` (`service`, `transport/http/v1/order`). Тесты `service` с моком
`OrderProvider`. Тесты `client/order.go` на `grpctest` по образцу Task 15.

**Acceptance criteria:**
- [x] Use case-ы gateway: успех и ошибка провайдера
- [x] Клиент: маппинг proto ↔ domain и кодов gRPC → `apperr`
- [x] Покрытие `service` ≥ 80%

**Verification:**
- [x] `cd services/gateway && go test -race -shuffle=on -count=1 -cover ./internal/service/ ./internal/client/`

**Dependencies:** Checkpoint 3

**Files likely touched:**
- `services/gateway/.mockery.yml`
- `services/gateway/internal/service/mocks/mocks.go`
- `services/gateway/internal/service/order_test.go`
- `services/gateway/internal/client/order_test.go`

**Estimated scope:** M

## Task 23: gateway — тесты HTTP-транспорта `v1/order`

**Description:** `httptest` + реальный роутер + мок `Service`: статусы и тела ответов, невалидный JSON → 400,
ошибки сервиса → HTTP-коды, `mapper` и `dto` (`Validate`).

**Acceptance criteria:**
- [x] Каждый endpoint: успех (статус + тело), невалидное тело, ошибка сервиса
- [x] Валидация `dto` покрыта таблицей
- [x] Запрос идёт через роутер, как в `app`

**Verification:**
- [x] `cd services/gateway && go test -race -shuffle=on -count=1 -cover ./internal/transport/...`

**Dependencies:** Task 22

**Files likely touched:**
- `services/gateway/internal/transport/http/v1/order/mocks/mocks.go`
- `services/gateway/internal/transport/http/v1/order/handler_test.go`
- `services/gateway/internal/transport/http/v1/order/dto_test.go`
- `services/gateway/internal/transport/http/v1/order/mapper_test.go`

**Estimated scope:** M

## Checkpoint 4: все сервисы покрыты
- [ ] `task test`, `task lint` из корня зелёные
- [ ] `task cover` зелёный по `domain` и `service` во всех сервисах
- [ ] `task mocks` без diff

---

## Phase 5: перевод оставшихся тестов

## Task 24: Перевод тестов `platform/telemetry`

**Description:** 9 файлов: stdlib-ассерты → `require`/`assert`, русские имена кейсов → английские. Поведение
кейсов не меняется. Механика, поэтому больше 5 файлов допустимо.

**Acceptance criteria:**
- [x] Количество кейсов до и после совпадает
- [x] Нет кириллицы в `name`/`t.Run`; покрытие `telemetry` не упало (91%)

**Verification:**
- [x] `cd platform && go test -race -shuffle=on -count=1 -cover ./telemetry/`

**Dependencies:** None

**Files likely touched:** `platform/telemetry/*_test.go`

**Estimated scope:** M (механика)

## Task 25: Перевод тестов `logger`, `postgres`, `grpc` (otel)

**Description:** `logger/logger_test.go`, `logger/otel_test.go`, `postgres` (unit-часть), `grpc/otel_test.go` —
на testify и английские имена.

**Acceptance criteria:**
- [ ] Количество кейсов до и после совпадает, покрытие не упало
- [ ] Нет кириллицы в `name`/`t.Run`

**Verification:**
- [ ] `cd platform && go test -race -shuffle=on -count=1 -cover ./logger/ ./postgres/ ./grpc/`

**Dependencies:** None

**Files likely touched:**
- `platform/logger/logger_test.go`
- `platform/logger/otel_test.go`
- `platform/postgres/postgres_test.go`
- `platform/grpc/otel_test.go`

**Estimated scope:** S

## Task 26: Перевод тестов конфигов сервисов и `outbox_test.go`

**Description:** `config_test.go` в 5 сервисах и `order/internal/repository/pg/outbox_test.go` — на testify
и английские имена.

**Acceptance criteria:**
- [ ] Количество кейсов до и после совпадает
- [ ] Нет кириллицы в `name`/`t.Run` во всём репозитории

**Verification:**
- [ ] `task test`
- [ ] `grep -rnE '(name:|t\.Run\()\s*"[^"]*[А-Яа-я]' platform services --include='*_test.go'` — пусто

**Dependencies:** None

**Files likely touched:** `services/*/internal/config/config_test.go`, `services/order/internal/repository/pg/outbox_test.go`

**Estimated scope:** M (механика)

---

## Phase 6: закрытие

## Task 27: Финальная проверка, покрытие в карте эпика, резюме багов

**Description:** Прогнать все Success Criteria спека. Записать итоговое покрытие по пакетам в
`SPEC-testing-map.md`, перевести статус `unit` в `done`, перечислить найденные и исправленные баги.

**Acceptance criteria:**
- [ ] Все пункты Success Criteria в `SPEC-unit.md` отмечены
- [ ] Покрытие по пакетам записано в карту эпика
- [ ] Резюме багов составлено (или явно «багов не найдено»)

**Verification:**
- [ ] `task test` при остановленном Docker → exit 0
- [ ] `task cover`, `task lint`, `task fuzz FUZZTIME=5s` зелёные
- [ ] `task mocks && git diff --exit-code`
- [ ] `grep` по кириллице в именах кейсов пуст; `grep -rn bufconn services` пуст (только `grpctest`)

**Dependencies:** Все задачи

Дополнительно: в README описать, как новый модуль попадает под контроль покрытия (сервис — копия
`.testcoverage.yml` с правилами `/domain$` и `/service$` и запись в `COVER_MODULES` корневого `Taskfile.yml`;
пакет `platform` — автоматически под 80%).

**Files likely touched:**
- `spec/testing/SPEC-testing-map.md`
- `spec/testing/SPEC-unit.md`
- `README.md`

**Estimated scope:** XS

## Checkpoint: Complete
- [ ] Все Success Criteria спека выполнены
- [ ] Готово к ревью
