# Spec: unit

> Эпик: [testing](SPEC-testing-map.md) · Статус: **draft** · Зависит от: [test-foundation](SPEC-test-foundation.md)

## Objective

Покрыть unit-тестами всю логику без I/O во всех сервисах и в `platform` и перевести существующие
тесты на конвенции `test-foundation` (testify, английские имена кейсов, `t.Parallel`).

Сейчас (снято `go test -cover`, без тега `integration`):

- `service`: тесты есть только в `order` (87.9%); в `user`, `product`, `notification`, `gateway` — 0%.
- `domain`, `event`, `client`, `transport` во всех сервисах — 0%.
- `platform`: высокое покрытие только у `telemetry` (91%) и `logger` (91%); `apperr*`, `appctx`, `closer`,
  `grpc*`, `interceptors`, `bind`, `response` — 0%; `middleware` 28%, `kafkaconsumer` 14%.
- 22 тестовых файла на stdlib без testify, в 11 файлах имена кейсов на русском.

Пользователь — разработчик проекта (и AI-агент). Результат:

- `task test` проверяет поведение каждого use case, маппинга и транспорта без Docker;
- `task cover` зелёный по порогам `domain` и `service` во всех сервисах;
- парсеры внешнего входа (HTTP-тело, Kafka-сообщения) проверены fuzz-тестами.

Вне скоупа (модуль `integration`): `repository/pg`, `postgres.Connect`/`TxManager` на реальной БД,
цикл чтения `kafkaconsumer` на Redpanda, миграции, `testkit`. Также вне скоупа: `cmd`, `app` (сборка
зависимостей), `docs`, `swagger`, e2e, нагрузка, CI.

## Tech Stack

Без новых зависимостей. Используется стек `test-foundation`: `testify`, `mockery` v3 (шаблон `testify`),
`goleak`, `go-test-coverage`. Дополнительно из stdlib и уже подключённых модулей:

| Что | Чем |
|---|---|
| HTTP-транспорт gateway, middleware | `net/http/httptest` |
| gRPC-транспорт и gRPC-клиенты | `google.golang.org/grpc/test/bufconn` (часть `grpc`, уже в `go.mod`) через хелпер `platform/testkit/grpctest` |
| Fuzz | нативный `testing.F` |
| Утечки горутин | `goleak.VerifyTestMain` в `main_test.go` пакета |

## Commands

Существующие команды без изменений:

```
task test               # go test -race -shuffle=on -count=1 ./... — включает seed-корпус fuzz-тестов
task cover              # пороги из .testcoverage.yml
task mocks              # mockery во всех модулях с .mockery.yml
task lint
```

Новая команда:

```
task fuzz                    # все fuzz-цели во всех модулях, FUZZTIME=30s на цель
task fuzz FUZZTIME=5m        # длительный ручной прогон
task -d platform fuzz        # один модуль
```

`go test -fuzz` принимает одну цель на пакет, поэтому в Taskfile модуля цели перечислены в переменной
`FUZZ_TARGETS` (`./http/bind:FuzzJSON,...`), задача обходит их по очереди. Найденные падения
сохраняются в `testdata/fuzz/<FuzzName>/` и коммитятся как регрессионный корпус.

## Project Structure

Расположение и имена файлов — по `test-foundation`. Что добавляется:

```
platform/testkit/grpctest/grpctest.go        → NewConn(t, register, opts...): in-memory gRPC-сервер на bufconn
services/<svc>/
  .mockery.yml                               → + пакеты transport (интерфейсы сервисов) и service
  internal/service/mocks/mocks.go            → моки зависимостей use case
  internal/transport/<...>/mocks/mocks.go    → моки сервисов для тестов транспорта
  internal/<pkg>/<file>_test.go              → unit-тесты
  internal/<pkg>/testdata/fuzz/<FuzzName>/   → регрессионный корпус fuzz
platform/<pkg>/main_test.go                  → goleak.VerifyTestMain в пакетах с горутинами
```

### platform/testkit/grpctest

```go
// NewConn поднимает gRPC-сервер на bufconn и возвращает подключённого клиента.
// Сервер и соединение закрываются в t.Cleanup.
func NewConn(t testing.TB, register func(*grpc.Server), opts ...grpc.ServerOption) *grpc.ClientConn
```

- Не знает о сервисах и интерцепторах: интерцепторы `platform` передаёт вызывающий через `opts`.
  Так соблюдается правило `test-foundation` «`testkit` не импортирует другие пакеты `platform`».
- Транспорт: `register` регистрирует реальный handler с моком сервиса. Клиенты: `register` регистрирует
  фейковый сервер на встроенном `Unimplemented*Server`, а клиент сервиса создаётся на возвращённом `conn`.
- Без Docker и сети: используется в unit-тестах, тега `integration` нет.
- Свой тест в `testkit`: вызов проходит до зарегистрированного сервера, после `Cleanup` горутины не утекают (`goleak`).

## Покрытие по пакетам

| Пакет | Что проверяем | Как |
|---|---|---|
| `*/internal/service` (user, product, notification, gateway, order — дополнить) | Каждый use case: успех, ошибка каждой зависимости, порядок вызовов в транзакции, идемпотентность inbox в `notification` | моки mockery, `TxManager` — ручной фейк |
| `order/internal/domain` | `NewOrder`: пустые позиции → `ErrOrderNoItems`, расчёт `TotalAmount`, ID позиций и `OrderID`, статус `created`, UTC-время; `OrderItem.Subtotal` | таблица |
| `order/internal/event` | `OrderCreated`: payload разбирается обратно в `orderv1.OrderCreated` с теми же полями, `snake_case`-имена, поля `OutboxEvent` | round-trip через `protojson` |
| `order/internal/client`, `gateway/internal/client` | Маппинг proto → domain, маппинг кодов gRPC → `apperr` через `grpcerr.Map`, передача аргументов | `bufconn` + фейковый сервер на сгенерированных `Unimplemented*Server` |
| `*/internal/transport/grpc` (order, user, product) | Маппинг request → вход use case и результата → response, ошибки `apperr` → коды gRPC через реальные интерцепторы `platform` | `bufconn` + `grpcserver`-интерцепторы + мок сервиса |
| `gateway/internal/transport/http/v1/order` | Статусы и тела ответов, невалидный JSON, ошибки сервиса → HTTP-коды, `mapper`/`dto` | `httptest` + роутер + мок сервиса |
| `notification/internal/transport/kafka` | Роутинг по заголовку типа события, неизвестный тип, битый payload → `InvalidArgument`, заполнение `InboxEvent` из сообщения | мок `NotificationService`, fuzz |
| `platform/apperr`, `grpcerr`, `ozzoerr`, `pgerr` | Конструкторы, `Wrap`, `Is`/`As`, маппинг кодов в обе стороны, ошибки валидации ozzo и коды `pgconn.PgError` | таблицы |
| `platform/appctx`, `closer`, `response`, `bind`, `otelenv` | Чистые функции, порядок и ошибки закрытия в `closer` | таблицы, fuzz для `bind` |
| `platform/grpc/interceptors`, `grpcclient`, `grpcserver` | Маппинг ошибок, логирование, валидация; старт/остановка сервера | `bufconn`, `goleak` в `grpcserver` |
| `platform/http/middleware`, `httpserver` | Дополнить: `fake_auth`, `logger`, `recover`, `route`; graceful shutdown | `httptest`, `goleak` в `httpserver` |
| `platform/kafkaconsumer` | Unit-часть: обработка сообщения, ошибки обработчика, `Message` | без брокера; цикл чтения — `integration` |
| `platform/telemetry`, `logger`, `postgres` (unit-часть), все `config` | Только перевод на testify и английские имена | — |

Пакеты `domain` без логики (только типы и константы) тестов не требуют: у них нет statements.

### Fuzz-цели

| Цель | Инвариант |
|---|---|
| `platform/http/bind` | Любой вход не вызывает панику; ошибка разбора — `apperr` с кодом `InvalidArgument` |
| `notification/.../kafka/order` `handleOrderCreated` | Любой `msg.Value` не вызывает панику; ошибка — `InvalidArgument`, сервис не вызывается |
| `order/internal/event` round-trip | Для произвольных полей заказа payload разбирается обратно в те же значения |

Seed-корпус — валидный пример и несколько граничных (пустой ввод, `null`, обрезанный JSON, лишние поля).

## Code Style

По `test-foundation` (таблицы, `t.Parallel`, `require`/`assert`, `t.Context()`, `fixtureX()`).
Образец теста транспорта на bufconn:

```go
func TestOrderHandler_GetOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(svc *mocks.MockOrderService)
		wantCode codes.Code
	}{
		{
			name: "maps not found error to NotFound code",
			setup: func(svc *mocks.MockOrderService) {
				svc.EXPECT().GetOrder(mock.Anything, "order-1").Return(domain.Order{}, apperr.NotFound())
			},
			wantCode: codes.NotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := mocks.NewMockOrderService(t)
			tt.setup(svc)
			conn := grpctest.NewConn(t,
				func(s *grpc.Server) { orderv1.RegisterOrderServiceServer(s, grpctransport.NewOrderHandler(svc)) },
				grpc.ChainUnaryInterceptor(interceptors.Error()), // интерцепторы как в app
			)
			client := orderv1.NewOrderServiceClient(conn)

			_, err := client.GetOrder(t.Context(), &orderv1.GetOrderRequest{Id: "order-1"})

			require.Equal(t, tt.wantCode, status.Code(err))
		})
	}
}
```

Дополнительно к конвенциям `test-foundation`:

- имена конструкторов и интерцепторов в образце условны: в тестах используются те же, что в `app` сервиса;
- прочие хелперы, нужные одному пакету, живут в `helpers_test.go` этого пакета;
- время и UUID в ассертах не сравниваются точно: проверяем формат (`uuid.Parse`), `Location() == time.UTC`, `WithinDuration`;
- при переводе существующих тестов поведение кейсов не меняется — только API ассертов и имена.

## Testing Strategy

Модуль сам состоит из тестов. Уровень — только unit: без внешней сети, без Docker,
без файловой системы вне `testdata/`. Каждый тест проходит под `-race -shuffle=on`.
gRPC-вызовы идут через in-memory `bufconn`. Жизненный цикл серверов (`grpcserver.Run`, `httpserver`)
проверяется на loopback-листенере `127.0.0.1:0`: так же работает `httptest.NewServer`, порт выдаёт ОС.

Порядок работы по пакету: тесты на текущее поведение → если тест выявил баг, сначала красный тест,
затем исправление (Prove-It) → `task cover` для пакета.

## Изменения конфигурации

- `platform/.testcoverage.yml`: правила `/domain$` и `/service$` удаляются — в `platform` таких пакетов нет,
  правила скопированы из сервисов и ни на что не влияют. Вместо них — порог 80% на каждый пакет без пороговых
  override:

```yaml
threshold:
  package: 80
  total: 70

exclude:
  paths:
    - (^|/)mocks/
```

  Новый пакет `platform` сразу попадает под 80%. I/O-пакеты (`postgres`, `testkit/pgtest`) покрываются интеграционными
  тестами: `task cover` и так идёт с Docker. Пороговые override не вводятся — они фиксируют низкое покрытие
  вместо того, чтобы его устранить. Недостижимая ветка вне I/O помечается `// coverage-ignore` с причиной.

- `.mockery.yml` добавляется в `gateway`, `user`, `product`, `notification`; в `order` в него добавляется пакет `transport/grpc`.
- `Taskfile.yml` в корне и в модулях: задача `fuzz` и переменная `FUZZ_TARGETS`.

## Boundaries

- **Always:**
  - тестировать через публичный API пакета (`package x_test`), white-box — только с обоснованием в `//nolint`;
  - моки только через `task mocks`, повторный запуск без diff;
  - найденный баг чинить в той же задаче вместе с тестом, который его доказывает, и явно перечислять исправленные баги в резюме задачи;
  - `task test` и `task lint` зелёные перед сдачей задачи.
- **Ask first:**
  - исправление, меняющее контракт (gRPC/HTTP-ответы, формат события, коды ошибок) — даже если это баг;
  - рефакторинг боевого кода сверх выноса интерфейса или шва для тестируемости;
  - новые зависимости, изменение порогов покрытия;
  - вынос в `platform/testkit` общих тестовых хелперов, кроме `grpctest`.
- **Never:**
  - Docker, реальная БД или Kafka в unit-тестах;
  - `t.Skip`, удаление или ослабление существующих кейсов при переводе на testify;
  - `time.Sleep` для синхронизации — только каналы, `require.Eventually` или `goleak`;
  - ручные моки на `testify/mock`;
  - `git commit` без команды пользователя.

## Success Criteria

- [ ] `task test` зелёный во всех модулях, работает без Docker.
- [ ] `task cover`: пакеты `domain` и `service` всех сервисов ≥ 80% (`total` ≥ 70% — общая цель с модулем `integration`, здесь не гейт).
- [ ] `task cover`: каждый пакет `platform` ≥ 80%, пороговых override нет.
- [ ] `platform/testkit/grpctest` используется в тестах транспорта `order`, `user`, `product` и клиентов `order`, `gateway`; копий bufconn-обвязки в сервисах нет.
- [ ] Каждый пакет из таблицы «Покрытие по пакетам» имеет тесты; итоговое покрытие по пакетам записано в карту эпика.
- [ ] Все тесты используют testify, ни одного русского `name` или имени `t.Run` (проверка: `grep` по кириллице в `name:` и `t.Run("` пуст).
- [ ] `goleak.VerifyTestMain` в `grpcserver` и `httpserver` зелёный.
- [ ] `task fuzz` запускает все три fuzz-цели; `task test` прогоняет их seed-корпус.
- [ ] `.mockery.yml` есть во всех сервисах, `task mocks` повторно не даёт diff.
- [ ] `task lint` зелёный.
- [ ] Найденные и исправленные баги перечислены в резюме модуля.

## Open Questions

1. ~~Общий хелпер bufconn~~ — решено: `platform/testkit/grpctest`, интерцепторы передаются через `opts`.
2. ~~Порог для пакетов `platform`~~ — решено: 80% на каждый пакет, без пороговых override; I/O-пакеты покрываются
   интеграционными тестами (см. «Изменения конфигурации»).
3. ~~Запуск `grpcserver.Run`/`httpserver` в unit-тестах~~ — решено: loopback `127.0.0.1:0`, см. Testing Strategy.
4. ~~Входные данные fuzz `order/internal/event`~~ — решено: `testing.F` принимает только примитивы, заказ собирается
   из них (id, суммы, строки, число позиций).
5. ~~Неизвестный тип события в `notification`~~ — решено: пропускается с `nil` (tolerant reader: новые типы
   на общем топике не должны блокировать консьюмер). Фиксируется тестом.
