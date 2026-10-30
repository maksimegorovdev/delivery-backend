# Spec: integration

> Status: **approved** · Depends on: [test-foundation](../../../specs/testing/test-foundation/spec.md)

## Objective

Покрыть интеграционными тестами весь код, который работает с реальными зависимостями: Postgres,
Redpanda и сетевые gRPC/HTTP-серверы. Также закрыть порог покрытия `total` ≥ 70% во всех модулях.
Модуль `unit` его осознанно оставил красным.

Сейчас (итоги `unit`, `task cover`): `total` — gateway 58.4%, order 69.0%, user 34.5%, product 32.0%,
notification 44.8%. Без тестов остались `app` и `repository/pg`. Интеграционные тесты есть только
у пилота `order` (`OrderRepo.Create`), `platform/postgres` (`New`, `DB`, `TxManager`) и smoke-тестов `testkit`.

Пользователь — разработчик проекта (и AI-агент). Результат:

- каждый репозиторий `pg` проверен на реальной схеме, включая ограничения БД и маппинг ошибок `pgerr`;
- миграции каждого сервиса проходят цикл up → down → up;
- атомарность заказа и outbox-события доказана откатом транзакции;
- контракт события `order.created` зафиксирован одним эталоном. Продюсер (`order`) пишет в outbox
  ровно его, консьюмер (`notification`) его корректно обрабатывает;
- путь Redpanda → `kafkaconsumer` → router → handler → service → Postgres в `notification` проверен
  целиком, вместе с идемпотентностью повторной доставки;
- `kafkaconsumer` проверен на настоящем брокере: коммит, откат оффсета и ретрай, остановка;
- `app` каждого сервиса собирается из env, обслуживает один запрос и корректно закрывается;
- `task cover` зелёный во всех модулях по всем порогам.

Вне скоупа: Debezium и сквозной путь между сервисами (модуль `e2e`), нагрузка, CI. Также вне скоупа
отсев poison-сообщений (skip/DLQ): это отдельная задача, здесь текущее поведение только фиксируется тестом.

Отличие от карты эпика: в карте указано «gRPC client ↔ server». Реальный клиент `order` не может
вызвать реальный сервер `user`/`product`: их пакеты `internal` недоступны из другого модуля.
Поэтому межсервисный вызов проверяется в `e2e`. Здесь его заменяют компонентные тесты gRPC-стека
каждого сервиса (handler → service → repo → Postgres) и smoke-тест `app`.

## Tech Stack

Новых внешних зависимостей нет. Используется стек `test-foundation`: `testify`, `testcontainers-go`
(через `platform/testkit`), `golang-migrate`, `goleak`, `go-test-coverage`, а также уже подключённые
`franz-go` (`kgo`) и `grpctest` из модуля `unit`.

| Что | Чем |
|---|---|
| Postgres | `pgtest.NewDB` (клон шаблона) и новый `pgtest.CheckMigrations` |
| Redpanda | `kafkatest.Brokers`, `kafkatest.CreateTopic` и новый `kafkatest.Produce` |
| gRPC-стек сервиса | `grpctest.NewConn` + интерцепторы `platform` + репозитории на `pgtest` |
| Сравнение с эталоном | `assert.JSONEq` / `require.JSONEq` |
| `app` | реальный `app.New` с env через `t.Setenv`, адрес `127.0.0.1:0`, фактический адрес через `App.Addr()` |

Для запуска нужен Docker, как и для всего `task test-integration`.

## Commands

Существующие команды без изменений:

```
task test-integration                          # go test -race -shuffle=on -count=1 -tags=integration ./... во всех модулях
task -d services/notification test-integration # один модуль
task cover                                     # unit + integration, пороги .testcoverage.yml — должен стать зелёным
task lint
```

Обновление эталона после осознанного изменения контракта:

```
go test -tags=integration -run TestOrderCreated_Golden ./internal/repository/pg -update   # из services/order
```

Флаг `-update` перезаписывает эталон фактическим значением. Без флага тест только сравнивает.
Тест консьюмера читает тот же файл, поэтому изменение контракта сразу проверяется и на стороне `notification`.

## Project Structure

Расположение и имена файлов — по `test-foundation`. Что добавляется:

```
contracts/events/order.created.json                 → эталон Kafka-сообщения order.created (см. «Контракт события»)
platform/testkit/pgtest/migrations.go               → CheckMigrations(t, dir)
platform/testkit/kafkatest/kafkatest.go             → + Produce(t, topic, records...)
platform/kafka/kafkaconsumer/kafkaconsumer_integration_test.go
services/<svc>/internal/repository/pg/
  <file>_integration_test.go                        → тест репозитория
  migrations_integration_test.go                    → pgtest.CheckMigrations
services/<svc>/internal/transport/grpc/<file>_integration_test.go   → компонентный gRPC-стек (order, user, product)
services/notification/internal/transport/kafka/consumer_integration_test.go → компонентный Kafka-стек
services/<svc>/internal/app/app_integration_test.go → smoke-тест App
```

### contracts/events

Нейтральный каталог контрактов событий в корне репозитория. Ни один сервис не читает файлы из
внутренностей другого. Один файл на тип события, имя — тип события (`order.created.json`).
Каталог не является Go-модулем: тесты читают файл по относительному пути от своего пакета
(константа `contractsDir` в `helpers_integration_test.go`).

### Шов App.Addr()

Нужен smoke-тесту, чтобы узнать фактический порт без гонки за свободный порт (TOCTOU). Применяется
в `gateway`, `order`, `user`, `product`; у `notification` нет листенера, он не меняется.

- `app.New` открывает листенер на адрес из конфига (`net.Listen`) и закрывает его через `closer`,
  если `New` упал позже. Адрес `127.0.0.1:0` в env даёт свободный порт от ОС;
- `Run` передаёт листенер в `grpcserver`/`httpserver` `Serve(ctx, lis)`, добавленный модулем `unit`;
- `Addr() net.Addr` возвращает `lis.Addr()`.

Поведение в продакшене не меняется. Ошибка «порт занят» теперь возникает в `New` (fail fast), а не в горутине `Run`.

### platform/testkit/pgtest.CheckMigrations

```go
// CheckMigrations создаёт пустую БД в общем контейнере, применяет все миграции из dir,
// откатывает их до нуля и применяет снова. Падает, если любой шаг вернул ошибку или
// после полного отката в схеме public осталось что-то кроме служебной таблицы migrate.
func CheckMigrations(tb testing.TB, migrationsDir string) {
```

- Использует тот же контейнер и то же правило «один каталог миграций на тестовый бинарь», что и `NewDB`.
- БД создаётся без `TEMPLATE` и удаляется в `tb.Cleanup`. Шаблон `template_app` не трогается.
- Свой тест в `testkit`: валидные миграции проходят; `down`, который оставляет таблицу, ловится.

### platform/testkit/kafkatest.Produce

```go
// Produce синхронно публикует записи в topic и падает при любой ошибке брокера.
func Produce(tb testing.TB, topic string, records ...*kgo.Record)
```

Нужен `kafkaconsumer` и `notification`. Иначе каждый тест заново собирает клиента и синхронный продюсер.

## Покрытие по пакетам

| Пакет | Что проверяем | Как |
|---|---|---|
| `order/internal/repository/pg` | `OrderRepo.Create`: дополнить нарушением `CHECK` (quantity ≤ 0) и дублем `id` → коды `apperr` из `pgerr`. `OutboxRepo.Save`: колонки и `traceparent`/`tracestate` при активном span и без него. Атомарность: `TxManager` + `Create` + `Save`, ошибка `Save` откатывает заказ и позиции | `pgtest`, реальный `postgres.TxManager` |
| `order` контракт | Эталон `order.created` (см. «Контракт события») | `pgtest` + `event.OrderEventBuilder` + `OutboxRepo` |
| `notification/internal/repository/pg` | `InboxRepo.Claim`: запись полей, повтор `event_id` не падает и не дублирует строку. `NotificationRepo.CreateForChannels`: строка на канал, повтор `(event_id, channel)` без дубля, пустой список каналов без запросов, недопустимый канал → ошибка `apperr` | `pgtest` |
| `user/internal/repository/pg` | `UserRepo.GetByID`, `AddressRepo.GetByID`: найдено, не найдено → `NotFound`, не-UUID id → ошибка `apperr` | `pgtest`, свои фикстуры |
| `product/internal/repository/pg` | `ProductRepo.GetByIDs`: все найдены, часть отсутствует, пустой список, не-UUID → ошибка `apperr` | `pgtest`, свои фикстуры |
| миграции всех сервисов | up → down → up без ошибок, после down схема пустая | `pgtest.CheckMigrations` |
| `*/internal/transport/grpc` (order, user, product) | Компонентный стек на реальной БД: запрос → ответ, запись видна в БД, ошибка БД (не найдено) → код gRPC. `order`: `CreateOrder` пишет заказ, позиции и outbox-событие; `user`/`product` — фейковые gRPC-серверы | `grpctest` + интерцепторы из `app` + `pgtest` |
| `notification/internal/transport/kafka` | Компонентный стек: эталон из `contracts/events` → Redpanda → consumer → строки в `inbox_events`/`notifications`. Повторная доставка того же события — без дублей. Неизвестный тип события коммитится, следующее сообщение обрабатывается. Невалидное сообщение не коммитится и ретраится (фиксация текущего поведения) | `kafkatest` + `pgtest` + реальные router/handler/service |
| `platform/kafka/kafkaconsumer` | Цикл `Run` на брокере: успешные записи коммитятся (новый consumer в той же группе их не получает). Ошибка хендлера откатывает оффсет, запись приходит снова после `retryBackoff`, следующие записи партиции не обрабатываются раньше неё. Отмена ctx завершает `Run`, `Close` без утечек (`goleak`) | `kafkatest` |
| `*/internal/app` (все 5 сервисов) | `app.New` из env → `Run` в горутине → один запрос на `App.Addr()` (gRPC `GetX`/`CreateOrder`, HTTP `GET` через gateway, одно Kafka-сообщение для notification) → отмена ctx → `Close` без ошибок | `t.Setenv`, `pgtest`/`kafkatest`, loopback-порты |

`platform/postgres` (`New`, `DB`, `TxManager`) уже покрыт интеграционными тестами из `test-foundation`
и не дополняется, если `task cover` не покажет пробелов.

### Контракт события

Зачем: каждая сторона сейчас тестирует только себя. `event` в `order` разбирает payload своим же кодом,
handler в `notification` читает JSON, написанный в тесте вручную. Изменение сериализации
(`UseProtoNames`, формат `created_at`) или конверта (имя заголовка в Debezium) оставит оба набора
тестов зелёными, а в рантайме сломает консьюмер. `DiscardUnknown` ещё и тихо отбросит переименованные
поля. `buf breaking` сравнивает только схему `.proto`. Общий эталон связывает обе стороны одним примером сообщения.

Эталон описывает сообщение так, как его видит консьюмер после Debezium `EventRouter`
(`deploy/debezium/order-outbox.json`), а не только строку outbox:

```json
{
  "key": "0192f0c1-0000-7000-8000-000000000001",
  "headers": { "event-type": "order.created" },
  "value": { "event_id": "…", "order_id": "…", "user_id": "…", "items": [ … ], "total_amount": "19000", "delivery_address": "…", "created_at": "…" }
}
```

- **Продюсер (`order`):** для фиксированного заказа `OrderEventBuilder` собирает событие, `OutboxRepo.Save`
  записывает его в БД. Тест читает строку и строит сообщение по правилам `EventRouter`:
  `aggregate_id` → `key`, `type` → `headers.event-type`, `payload` → `value`. Результат сравнивается
  с эталоном через `JSONEq`. `event_id` генерируется (`uuid.NewV7`), поэтому перед сравнением
  подставляется значение из эталона, а формат проверяется отдельно (`uuid.Parse`, версия 7).
  `traceparent`/`tracestate` в эталон не входят: они необязательны и проверяются в тесте `OutboxRepo.Save`.
- **Консьюмер (`notification`):** компонентный Kafka-тест публикует эталон в Redpanda как есть
  (`key`, `headers`, `value`) и проверяет строки в БД. `payload` в `notifications` совпадает с `value` эталона (`JSONEq`).
- Правила `EventRouter` в тесте продюсера — копия конфигурации Debezium. Реальное преобразование проверяет `e2e`.

## Code Style

По `test-foundation` и `unit`. Образец компонентного Kafka-теста:

```go
//go:build integration

package kafka_test

func TestConsumer_OrderCreated(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, migrationsDir)
	topic := kafkatest.CreateTopic(t, order.Topic)
	runConsumer(t, pool, topic) // реальные repo → service → router, Run в горутине, остановка в t.Cleanup

	msg := goldenRecord(t) // contracts/events/order.created.json → *kgo.Record
	kafkatest.Produce(t, topic, msg, msg) // повторная доставка того же события

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, count(t, pool, `SELECT count(*) FROM inbox_events`))
		assert.Equal(c, 1, count(t, pool, `SELECT count(*) FROM notifications`))
	}, eventuallyTimeout, eventuallyTick)
}
```

Дополнительно к конвенциям `test-foundation` и `unit`:

- тесты с `t.Setenv` (smoke `app`) не параллельны: `//nolint:paralleltest // t.Setenv`;
- данные — только свои фикстуры с фиксированными UUID. На seed-миграции `user`/`product` тесты
  не опираются и их строки не считают: `count(*)` проверяется по своим id;
- асинхронные проверки только через `require.EventuallyWithT` с константами таймаута в `helpers_test.go` пакета;
- для каждого топика и consumer group в тесте — уникальное имя (`kafkatest.CreateTopic`, суффикс группы),
  чтобы параллельные тесты не делили оффсеты;
- общие для пакета хелперы (`runConsumer`, `goldenRecord`, `count`) живут в `helpers_integration_test.go` под тем же тегом.

## Testing Strategy

Модуль сам состоит из тестов. Уровень — integration: реальные Postgres и Redpanda из `testkit`,
без docker compose и без внешней сети. Все тесты под тегом `integration`, `-race -shuffle=on`,
`t.Parallel` везде, кроме тестов с `t.Setenv`. Изоляция: своя БД на тест (`pgtest.NewDB`),
свой топик и consumer group на тест.

Порядок работы по пакету: тесты на текущее поведение. Если тест выявил баг — сначала красный тест,
затем исправление (Prove-It). В конце — `task cover` для модуля.

## Изменения конфигурации

- `.testcoverage.yml` не меняется: пороги и исключения остаются как есть.
- `Taskfile.yml` не меняется.
- В `platform/testkit` добавляются `pgtest.CheckMigrations` и `kafkatest.Produce` (согласовано этим спеком).
- Новый каталог `contracts/events/` в корне репозитория.
- Боевой код: шов `App.Addr()` в `gateway`, `order`, `user`, `product` (согласовано этим спеком).

## Boundaries

- **Always:**
  - тег `//go:build integration` на каждом файле, который поднимает контейнер или слушает порт;
  - контейнеры только через `platform/testkit`;
  - изменение эталона — только через `-update` и только вместе с осознанным изменением контракта,
    упомянутым в резюме задачи;
  - найденный баг чинить в той же задаче вместе с доказывающим тестом и перечислять в резюме;
  - `task test`, `task test-integration` и `task lint` зелёные перед сдачей задачи.
- **Ask first:**
  - исправление, меняющее контракт (формат события, коды ошибок, gRPC/HTTP-ответы), даже если это баг;
  - изменения боевого кода сверх шва `App.Addr()` и шва для тестируемости;
  - изменение миграций, `docker-compose.yml`, конфигурации Debezium;
  - новые зависимости, изменение порогов или исключений покрытия;
  - новые хелперы в `platform/testkit` сверх `CheckMigrations` и `Produce`.
- **Never:**
  - подключение к БД или Kafka из docker compose;
  - `time.Sleep` для синхронизации — только `require.EventuallyWithT`, каналы, `goleak`;
  - `t.Skip`, ослабление или удаление существующих кейсов;
  - опора на seed-данные миграций;
  - `git commit` без команды пользователя.

## Success Criteria

- [x] `task test-integration` зелёный во всех модулях; `task test` по-прежнему работает без Docker.
- [x] `task cover` зелёный во всех модулях: `domain`/`service` ≥ 80%, каждый пакет `platform` ≥ 80%, `total` ≥ 70%.
- [x] Каждый пакет из таблицы «Покрытие по пакетам» имеет интеграционные тесты; покрытие `repository/pg` и `app` каждого сервиса записано в карту эпика.
- [x] `pgtest.CheckMigrations` вызывается во всех четырёх сервисах с миграциями; его тест ловит неполный `down`.
- [x] Тест атомарности `order` доказывает: ошибка `OutboxRepo.Save` не оставляет строк в `orders` и `order_items`.
- [x] Эталон `order.created` один и лежит в `contracts/events/`. Тест продюсера его проверяет, тест консьюмера его использует. Ручная правка поля в эталоне роняет тест продюсера.
- [x] Компонентный Kafka-тест `notification` доказывает: событие обрабатывается, повторная доставка не создаёт дублей, неизвестный тип не блокирует партицию.
- [x] Тесты `kafkaconsumer` на Redpanda покрывают коммит, откат с ретраем, остановку по ctx; `goleak` зелёный.
- [x] Smoke-тест `app` есть во всех пяти сервисах; `gateway`, `order`, `user`, `product` отдают `Addr()`, тесты не ищут свободный порт заранее.
- [x] `task lint` зелёный.
- [x] Найденные и исправленные баги перечислены в резюме модуля.

## Open Questions

1. ~~Где хранить эталон~~ — решено: `contracts/events/order.created.json` в корне репозитория. `proto` хранит
   только схемы и сгенерированный код, а у продюсера файл привязал бы консьюмер к его внутренней структуре.
2. ~~Адреса в smoke-тесте `app`~~ — решено: шов `App.Addr()` (см. «Шов App.Addr()»). Поиск свободного порта заранее
   отвергнут из-за гонки TOCTOU.
