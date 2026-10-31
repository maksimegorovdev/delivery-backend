# Spec: e2e

> Status: **approved** · Depends on: [integration](../../../specs/testing/integration/spec.md)

## Objective

Проверить систему целиком, как её видит клиент: HTTP-запрос в `gateway` проходит
gateway → order → user/product (gRPC) → `outbox_events` → Debezium `EventRouter` → Redpanda →
notification → `inbox_events`/`notifications`. Модуль `integration` проверил каждый сервис отдельно,
а правила `EventRouter` — копией конфигурации. Здесь проверяется то, чего не видит ни один
интеграционный тест: реальные сетевые вызовы между сервисами, реальное преобразование Debezium,
сквозной трейс и поведение при остановке компонентов.

Пользователь — разработчик проекта (и AI-агент). Результат:

- одна команда `task e2e` поднимает изолированный стек из docker compose (инфраструктура и все пять
  сервисов из образов), прогоняет тесты и убирает стек;
- happy path доказан: заказ создаётся через HTTP, уведомление появляется в БД `notification`,
  его `payload` согласован с ответом API;
- ошибки клиента и «не найдено» доходят через всю цепочку с правильным HTTP-статусом;
- один `trace_id` от входящего HTTP-запроса до обработки в `notification` виден в ClickHouse;
- остановка `notification` или Debezium не теряет события, повторная доставка не создаёт дублей.

Вне скоупа: нагрузка (модуль `load`), CI (решение карты: только Taskfile), проверка Kafka-сообщения
против эталона `contracts/events/` (решено: проверяем только результат в БД `notification`),
отсев poison-сообщений (skip/DLQ), отправка уведомлений по каналам (воркера отправки нет,
`status` остаётся `pending` и не проверяется).

### Отличия от карты эпика

- В карте сказано «стек из docker compose». Сервисов в `compose.yaml` сейчас нет, они запускаются
  на хосте через `air`. Модуль добавляет Dockerfile сервисов и отдельный compose-файл для e2e.
- «Чёрный ящик» соблюдается на входе: тест ходит только в публичный HTTP API `gateway`. На выходе
  у `notification` нет API, поэтому результат читается SQL-запросами только на чтение из его БД.
  Так же, только на чтение, тест берёт id seed-данных из `user`/`product`: публичного API для списка
  товаров и адресов нет.

## Tech Stack

| Что | Чем |
|---|---|
| Стек | Docker Compose, отдельный файл `e2e/compose.yaml` и проект `delivery-e2e` |
| Образы сервисов | Один multi-stage Dockerfile: `golang:1.27` → `gcr.io/distroless/static-debian12:nonroot`, `ARG SERVICE` |
| Миграции в стеке | Одноразовые контейнеры `migrate/migrate` (версия как у `golang-migrate` в Taskfile сервисов) |
| Регистрация Debezium | Одноразовый контейнер `curlimages/curl:8.15.0`, тот же `deploy/debezium/order-outbox.json` |
| Тесты | Go-модуль `e2e/`, тег `e2e`, `testify` (`require`, `assert`, `EventuallyWithT`) |
| Чтение БД | `pgx/v5` (уже в workspace), только `SELECT` |
| Готовность сервисов | gRPC health (`google.golang.org/grpc/health/grpc_health_v1`, уже в workspace). Сервер health регистрирует `platform/grpc/grpcserver` (см. «Изменения конфигурации») |
| ClickHouse | HTTP-интерфейс (`net/http`), пользователь `grafana_reader`, без нового клиента |
| Управление стеком из тестов | `docker compose` через `os/exec` (stop/start/exec) |

Новые Go-зависимости не добавляются: всё есть в workspace. Новые Docker-образы — `golang`,
`distroless/static`, `migrate/migrate`, `curlimages/curl`, все с фиксированными версиями
(согласовываются этим спеком).

## Commands

```
task e2e                # e2e-up → e2e-test → e2e-down (down выполняется и при падении тестов)
task e2e-up             # e2e-images-check, затем docker compose -f e2e/compose.yaml -p delivery-e2e up -d --build --wait
task e2e-images-check   # версии образов инфраструктуры в e2e/compose.yaml совпадают с корневым compose.yaml
task e2e-test           # go test -race -shuffle=on -count=1 -tags=e2e ./... в e2e/ (стек уже поднят)
task e2e-down           # docker compose -f e2e/compose.yaml -p delivery-e2e down -v
task e2e-logs           # логи сервисов стека, для разбора падений
task -d e2e lint        # golangci-lint модуля
```

`E2E_KEEP=1 task e2e` не убирает стек после прогона: можно разобрать падение (`task e2e-logs`,
Grafana не входит в стек, трейсы читаются прямо из ClickHouse).

`task test` и `task test-integration` не запускают e2e: без тега `e2e` в модуле нет тестов.

## Project Structure

```
deploy/docker/service.Dockerfile   → общий Dockerfile сервисов (ARG SERVICE), контекст — корень репозитория
.dockerignore                      → bin/, tmp/, coverage/, .git/, spec/
e2e/
  go.mod                           → модуль github.com/maksimegorovdev/delivery-backend/e2e, в go.work
  Taskfile.yml                     → tools, lint, test (компиляция без тега), tidy
  .golangci.yml                    → как у сервисов
  compose.yaml                     → стек e2e (см. «Стек»)
  env.go                           → адреса стека (константы с портами e2e, переопределяются env)
  stack_e2e_test.go                → TestMain: ожидание готовности, seed-id; хелперы stop/start/exec
  helpers_e2e_test.go              → HTTP-клиент gateway, запросы к БД, ClickHouse, константы таймаутов
  order_e2e_test.go                → happy path и ошибки API
  trace_e2e_test.go                → сквозной трейс
  resilience_e2e_test.go           → остановка компонентов, повторная доставка
```

Тесты лежат в одном пакете `e2e_test`: модуль маленький, разбивать на пакеты незачем.

### Стек (`e2e/compose.yaml`)

Проект `delivery-e2e` изолирован от dev-стека: свои контейнеры, сети и порты, без volumes.
Оба стека могут работать одновременно.

- **Инфраструктура** описана в `e2e/compose.yaml` самостоятельно, без `extends`: 4 Postgres, `redpanda`,
  `debezium`, `clickhouse`, `otel-collector`. Без `redpanda-console` и `grafana`, без `container_name`,
  без томов с данными (после `down -v` ничего не остаётся). Конфиги монтируются из общих файлов
  репозитория (`../deploy/clickhouse/users.d/otel-users.xml`, `../deploy/otel-collector/config.yaml`),
  чтобы не копировать их. Порты — диапазон e2e.
- **Версии образов** инфраструктуры совпадают с корневым `compose.yaml`. Это проверяет
  `task e2e-images-check`: каждый образ из `docker compose -f e2e/compose.yaml config --images`, кроме
  образов сервисов и одноразовых контейнеров, должен быть в `docker compose config --images` корневого
  файла. `task e2e-up` вызывает проверку первой, поэтому расхождение версий роняет запуск.
- **Healthchecks:** `pg_isready` для Postgres, `rpk cluster health` для Redpanda, `curl /connectors`
  для Debezium, у ClickHouse — как в корневом файле.
- **Миграции:** по одному `migrate-<svc>` на каждую БД, `depends_on: service_healthy`. Сервис стартует
  после `service_completed_successfully` своей миграции.
- **Debezium:** `debezium-register` после `debezium: service_healthy` делает тот же `PUT`, что и
  `task connect-register`, и завершается с ошибкой, если ответ не 2xx.
- **Сервисы:** `gateway`, `order`, `user`, `product`, `notification` собираются из
  `deploy/docker/service.Dockerfile` с `SERVICE=<svc>`. Конфигурация — env в compose (адреса по
  именам сервисов в сети compose), `OTEL_ENABLED=true`, `LOG_FORMAT=json`.
- **Порты на хосте** (только то, что нужно тесту):

  | Что | Порт |
  |---|---|
  | gateway HTTP | 28080 |
  | order / user / product gRPC (health) | 28090 / 28091 / 28092 |
  | user / product / notification Postgres | 25433 / 25434 / 25435 |
  | Debezium REST | 28083 |
  | ClickHouse HTTP | 28123 |

`up --wait` ждёт healthchecks инфраструктуры и завершения одноразовых контейнеров. Готовность
сервисов проверяет `TestMain` (у distroless-образов нет утилит для healthcheck).

### TestMain

Перед тестами, с общим таймаутом 2 минуты:

1. Коннектор `order-outbox` в Debezium в состоянии `RUNNING` (`GET /connectors/order-outbox/status`).
2. gRPC health `SERVING` у `order`, `user`, `product`.
3. `gateway` отвечает `400` на `POST /api/v1/orders` с пустым телом.
4. Seed-id: id адреса seed-пользователя (`FAKE_AUTH_USER_ID`) из `user`, id двух seed-товаров
   по имени из `product`. Находятся `SELECT`-ом и кладутся в пакетные переменные.

Если шаг не прошёл — `TestMain` печатает, какой именно, и завершается с кодом 1, без запуска тестов.
Готовность `notification` отдельно не проверяется: консьюмер читает с начала (`AtStart`), событие не теряется.

### Dockerfile

```dockerfile
# Контекст — корень репозитория: сервисы ссылаются на platform и proto через replace ../../
FROM golang:1.27 AS build
ARG SERVICE
WORKDIR /src
COPY . .
RUN --mount=type=cache,target=/root/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    cd services/${SERVICE} && CGO_ENABLED=0 go build -trimpath -o /out/app ./cmd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
```

Набросок: точный порядок `COPY` (сначала `go.mod`/`go.sum` для кеша) и версии образов — в плане.
Dev-запуск через `air` не меняется; Dockerfile пригодится и для `load`.

## Сценарии

| Файл | Тест | Что проверяем |
|---|---|---|
| `order_e2e_test.go` | `TestCreateOrder_HappyPath` | `POST /api/v1/orders` с двумя seed-товарами → `201`. Ответ: `user_id` = seed-пользователь, позиции с именами и ценами из `product`, `total_amount` = Σ `unit_price × quantity`, `delivery_address` = адрес seed. Затем в БД `notification` (по `aggregate_id` = id заказа): ровно одна строка `inbox_events` с `event_type = 'order.created'` и ровно одна строка `notifications` с `channel = 'log'`. `payload` совпадает с ответом API по `order_id`, `user_id`, `items`, `total_amount`, `delivery_address` |
| | `TestCreateOrder_ClientErrors` | Таблица → `400` и код ошибки в конверте ответа: невалидный JSON, нет `items`, `address_id` не UUID, `quantity = 0` |
| | `TestCreateOrder_NotFound` | Таблица → `404`: несуществующий товар (валидный UUID), несуществующий адрес. Ошибка пришла из `product`/`user` через `order` и `gateway` |
| `trace_e2e_test.go` | `TestTrace_EndToEnd` | Тест отправляет свой заголовок `traceparent` со случайным `trace_id`. `gateway` — публичная граница (`httpserver.WithPublicEndpoint`): клиентский контекст не становится родителем, а прикрепляется ссылкой. В `otel.otel_traces` находится корневой спан `gateway` (`ParentSpanId = ''`), у которого `Links.TraceId` содержит клиентский `trace_id`. Под `TraceId` этого спана есть спаны всех пяти сервисов (`ServiceName`), спан `notification` — не корень. Ни одного спана с `TraceId` клиента нет: граница не принимает чужой trace-id |
| `resilience_e2e_test.go` | `TestResilience_NotificationDown` | `notification` остановлен → заказ создан (`201`) → `notification` запущен → событие обработано ровно один раз |
| | `TestResilience_DebeziumDown` | Debezium остановлен → заказ создан → Debezium запущен → событие доставлено и обработано ровно один раз (слот репликации держит WAL) |
| | `TestResilience_Redelivery` | Создать заказ, дождаться обработки. Остановить `notification`, сдвинуть оффсет группы `notification` в начало (`rpk group seek --to start` через `docker compose exec redpanda`), запустить. Все события перечитаны, но для заказа по-прежнему одна строка в `inbox_events` и одна в `notifications` |

«Ровно один раз» проверяется так: дождаться строки через `EventuallyWithT`, затем убедиться, что
счётчик не вырос за окно стабилизации (`stableWindow`, 3 с). Это единственное место, где ожидание по
времени допустимо: доказывается отсутствие события.

Тесты устойчивости не параллельны (`//nolint:paralleltest // останавливает общий контейнер`).
В Go параллельные тесты верхнего уровня ждут завершения всех последовательных, поэтому остановка
контейнера не задевает остальные. Каждый тест сам возвращает контейнер в рабочее состояние через
`t.Cleanup` и ждёт готовности.

## Code Style

По `test-foundation`, `unit`, `integration`. Образец:

```go
//go:build e2e

package e2e_test

func TestCreateOrder_HappyPath(t *testing.T) {
	t.Parallel()

	req := createOrderRequest{
		AddressID: seed.AddressID,
		Items:     []orderItem{{ProductID: seed.Borjomi.ID, Quantity: 2}, {ProductID: seed.Cola.ID, Quantity: 1}},
	}

	order := createOrder(t, req) // POST /api/v1/orders, require 201, разбор конверта

	require.Equal(t, seed.UserID, order.UserID)
	require.EqualValues(t, 2*seed.Borjomi.Price+seed.Cola.Price, order.TotalAmount)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		n, err := notificationsByOrder(t.Context(), order.ID)
		if !assert.NoError(c, err) {
			return
		}
		assert.Len(c, n, 1)
	}, eventuallyTimeout, eventuallyTick)
}
```

Дополнительно:

- имя файла `*_e2e_test.go`, тег `//go:build e2e` на каждом файле модуля, кроме `env.go`;
- адреса стека — только из `env.go`, без захардкоженных портов в тестах;
- каждый тест создаёт свои заказы и проверяет только их (по id заказа); общие счётчики таблиц не используются;
- таймауты — константы в `helpers_e2e_test.go`: `eventuallyTimeout` 30 с для `notification`,
  `traceTimeout` 60 с для ClickHouse (батчинг коллектора), `stableWindow` 3 с;
- SQL — только `SELECT` с параметрами, пул подключается к БД с `default_transaction_read_only=on`.

## Testing Strategy

Модуль сам состоит из тестов. Уровень — e2e: полный стек в docker compose, тест — внешний клиент.
`-race -shuffle=on -count=1`, `t.Parallel` везде, кроме тестов устойчивости.

Порядок работы: стек и `TestMain` → happy path → ошибки → трейс → устойчивость. Если тест выявил
баг — красный тест, затем исправление (Prove-It), по правилам «Ask first» ниже.

Покрытие e2e не считается: модуль не входит в `COVER_MODULES`.

Проверка флейков: `task e2e-test` с `-count=3` на поднятом стеке проходит без падений до сдачи модуля.

## Изменения конфигурации

- Новые файлы: `deploy/docker/service.Dockerfile`, `.dockerignore`, модуль `e2e/` (в `go.work`).
- `Taskfile.yml`: задачи `e2e`, `e2e-up`, `e2e-test`, `e2e-down`, `e2e-logs`, `e2e-images-check`;
  `e2e` добавляется в `MODULES` (lint, tidy, tools), но не в `COVER_MODULES` и не в `FUZZ_MODULES`.
- Корневой `docker-compose.yml` переименован в `compose.yaml` (единое имя с `e2e/compose.yaml`,
  каноническое для Compose Specification); содержимое не меняется. При обновлении версии образа в нём
  ту же версию нужно поставить в `e2e/compose.yaml`, иначе `task e2e-up` упадёт на `e2e-images-check`.
- `platform/grpc/grpcserver`: `New` регистрирует стандартный сервер health (`health.NewServer()`, статус `SERVING`), graceful stop сначала переводит его в `NOT_SERVING`. С unit-тестом. Фильтр телеметрии для health в `grpcserver` уже есть. Других изменений боевого кода нет.

## Boundaries

- **Always:**
  - тест обращается к системе только через HTTP `gateway`; БД читаются только `SELECT`-ом,
    gRPC — только health, ClickHouse — только чтение;
  - стек только через `task e2e*` и проект `delivery-e2e`;
  - асинхронные проверки через `require.EventuallyWithT`;
  - тест устойчивости возвращает остановленный контейнер в `t.Cleanup`;
  - найденный баг — с доказывающим тестом, перечислить в резюме;
  - `task test`, `task lint`, `task e2e` зелёные перед сдачей задачи.
- **Ask first:**
  - любое изменение боевого кода сервисов, включая исправление найденного бага;
  - изменение корневого `compose.yaml`, конфигурации Debezium, миграций;
  - новые Go-зависимости или Docker-образы сверх перечисленных в «Tech Stack»;
  - новые сценарии сверх таблицы «Сценарии».
- **Never:**
  - запись в БД, Kafka или ClickHouse из теста (кроме сдвига оффсета в `TestResilience_Redelivery`);
  - подключение к dev-стеку (`compose.yaml` с портами 5432–5435, 19092, 8080);
  - `time.Sleep` для синхронизации (кроме `stableWindow` для доказательства отсутствия события);
  - `t.Skip`, ослабление или удаление кейсов;
  - `git commit` без команды пользователя.

## Success Criteria

- [ ] `task e2e` на чистой машине с Docker поднимает стек, проходит и убирает его; повторный запуск тоже зелёный.
- [ ] Стек e2e и dev-стек (`task compose-up`) работают одновременно без конфликтов портов и имён.
- [ ] Все тесты из таблицы «Сценарии» есть и зелёные.
- [ ] Happy path доказывает: `payload` уведомления совпадает с ответом API, Debezium реально выполнил преобразование.
- [ ] `TestTrace_EndToEnd` находит спаны всех пяти сервисов под одним `trace_id`.
- [ ] Тесты устойчивости доказывают: нет потерь при остановке `notification`/Debezium и нет дублей при повторной доставке.
- [ ] `task e2e-test -count=3` на поднятом стеке без флейков (в том числе повторные прогоны без `e2e-down` между ними).
- [ ] `task test`, `task test-integration`, `task lint` по-прежнему зелёные; `e2e` не влияет на `task cover`.
- [ ] Итоги модуля (время прогона, найденные баги) записаны в карту эпика.

## Open Questions

1. ~~Seed-id через SQL~~ — решено: тест находит id адреса и товаров `SELECT`-ом по email
   seed-пользователя и именам товаров (см. «TestMain»). Миграции не меняются. Новая миграция с
   фиксированными UUID отвергнута: она меняет схему данных ради теста и затрагивает dev-БД.
2. ~~`extends` или отдельный файл~~ — решено: отдельный `e2e/compose.yaml`. `extends` отвергнут:
   `volumes: !reset` снимает и монтирование конфигов (ClickHouse, OTel Collector), правила слияния
   портов и путей неочевидны, а любая правка dev-стека (новый том, порт) молча меняет тестовое
   окружение. Дублирование версий образов закрывает `task e2e-images-check`.
3. ~~Стек на прогон или долгоживущий~~ — решено: оба режима. `task e2e` даёт чистый стек на каждый
   прогон (эталонный запуск, будущий CI). `task e2e-up` + `task e2e-test` — для частых прогонов
   при разработке. Тесты не зависят от состояния стека: каждый проверяет только свои заказы, поэтому
   повторные прогоны на долгоживущем стеке корректны.
4. ~~Готовность gRPC-сервисов~~ — решено: health регистрируется в `platform/grpc/grpcserver`. Сейчас
   его нет ни в одном сервисе. Проба через `404` от `gateway` отвергнута: она проверяет цепочку
   целиком, но не говорит, какой сервис не готов. Health нужен и модулю `load`.
5. ~~Сквозной трейс при публичной границе~~ — решено: трейс ищется через ссылку (`Links`) корневого
   спана `gateway` на клиентский `trace_id`. Ослаблять `WithPublicEndpoint` ради теста не будем.
