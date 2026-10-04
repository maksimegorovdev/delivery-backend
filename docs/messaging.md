# Асинхронные события: Outbox + Debezium + Redpanda + Inbox

Документ описывает, как ввести в `delivery-backend` событийную интеграцию: `order-service`
публикует факт создания заказа, `notification-service` его получает и (пока) печатает в терминал.

Статус: дизайн-документ. Код пишется руками по этому описанию, сниппеты — скелеты,
а не готовые файлы.

Версии, на которые ориентируемся (проверено на 2026-09-22):

| Компонент | Версия |
|---|---|
| Redpanda | `redpandadata/redpanda:v26.2.3` |
| Redpanda Console | `redpandadata/console:v3.12.0` |
| Debezium Connect | `quay.io/debezium/connect:3.6.3.Final` |
| PostgreSQL | `postgres:18` (как сейчас) |
| Kafka-клиент в Go | `github.com/twmb/franz-go v1.22.0` |

---

## 1. Что решаем

При создании заказа надо сделать две вещи атомарно:

1. записать заказ в `orderdb` (`orders` + `order_items`);
2. опубликовать событие `OrderCreated` в брокер.

Наивный вариант «сначала commit, потом produce» ломается ровно посередине: процесс упал между
commit и produce → заказ есть, события нет, потребитель о заказе не узнает никогда. Обратный
порядок не лучше: событие ушло, транзакция откатилась → событие про несуществующий заказ.

Двухфазный коммит между PostgreSQL и Kafka — не вариант (Kafka его не поддерживает, и это
антипаттерн в микросервисах). Решение — **transactional outbox**: событие пишется в ту же
БД, в той же транзакции, что и заказ. Дальше отдельный механизм вычитывает outbox-таблицу
и доставляет события в брокер.

На стороне потребителя зеркальная проблема: брокер даёт **at-least-once**, дубликаты неизбежны
(ребаланс, повтор после падения до коммита оффсета). Решение — **inbox**: потребитель ведёт
таблицу обработанных `event_id` и отбрасывает повторы, причём дедупликация и бизнес-эффект
происходят в одной транзакции.

### Почему CDC (Debezium), а не воркер-поллер

Классическая альтернатива — воркер в `order-service`, который раз в N мс делает
`SELECT ... FROM outbox_events WHERE published_at IS NULL FOR UPDATE SKIP LOCKED`, шлёт в Kafka
и проставляет `published_at`.

| | Воркер-поллер | Debezium (CDC по WAL) |
|---|---|---|
| Нагрузка на БД | постоянные запросы вхолостую | нет запросов, чтение WAL |
| Задержка | = интервал поллинга (10–500 мс) | ~единицы мс |
| Порядок | нужно самому держать (сортировка + одна нить на ключ) | порядок WAL, ключ → партиция |
| Код в сервисе | воркер, ретраи, лидер-элекшен при нескольких репликах | нет кода вообще |
| Инфраструктура | ничего лишнего | Kafka Connect + слот репликации + мониторинг лага |
| Отладка | тривиальная | нужен Console/REST Connect, читать логи коннектора |

Выбираем Debezium: сервис вообще не знает про Kafka (его зависимость — только своя БД), доставка
и ретраи — забота Connect. Плата — инфраструктурная сложность и новый класс проблем
(replication slot, его лаг, поведение при рестарте). Раздел 10 — про то, как с этим жить.

---

## 2. Итоговый поток

```
                      ┌──────────────── order-service (Go) ────────────────┐
  gRPC CreateOrder →  │ usecase.CreateOrder                                │
                      │   txManager.WithinTx(ctx, func(ctx) error {        │
                      │       orders.Create(ctx, order)   → orders,        │
                      │                                     order_items    │
                      │       outbox.Save(ctx, event)     → outbox_events  │
                      │   })                                               │
                      └────────────────────────┬───────────────────────────┘
                                               │ один COMMIT
                                               ▼
                                     ┌──────────────────┐
                                     │  order-postgres  │
                                     │ wal_level=logical│
                                     └─────────┬────────┘
                                               │ WAL (pgoutput, replication slot)
                                               ▼
                            ┌───────────────────────────────────┐
                            │ Kafka Connect + Debezium PG        │
                            │ SMT: outbox.EventRouter            │
                            │  key   = aggregate_id              │
                            │  value = payload (jsonb → JSON)    │
                            │  header= event-type, id            │
                            │  topic = ${aggregate_type}.events.v1│
                            └───────────────┬───────────────────┘
                                            ▼
                                  ┌───────────────────┐
                                  │  Redpanda         │
                                  │  order.events.v1  │  1 партиция
                                  └─────────┬─────────┘
                                            │ consumer group notification
                                            ▼
                ┌───────────────── notification-service (Go) ──────────────┐
                │ transport/kafka  → usecase.HandleOrderCreated            │
                │   txManager.WithinTx(ctx, func(ctx) error {              │
                │       ok := inbox.Claim(ctx, event)      // ON CONFLICT DO NOTHING
                │       if !ok { return nil }        // дубль — выходим     │
                │       return notifications.Create(ctx, ...) // pending   │
                │   })                                                     │
                │ commit offset только после успешной транзакции           │
                │                                                          │
                │ worker (тикер): notifications pending → Send → sent      │
                │                 (пока канал log)                         │
                └──────────────────────────────────────────────────────────┘
```

Ключевое свойство: **ни в одном месте нет шага «записал в БД, а потом отдельно сходил в сеть»**,
который мог бы порваться посередине без возможности восстановления.

---

## 3. Контракт события

### 3.1 Где описывается

Контракт — в `proto/`, рядом с остальными (см. `proto/proto/order/v1/`). Отдельный файл,
чтобы события не смешивались с API сервиса:

```
proto/proto/order/v1/events.proto
```

```proto
syntax = "proto3";

package order.v1;

import "google/protobuf/timestamp.proto";

// Событие: заказ создан. Публикуется через outbox.
message OrderCreated {
  // Идентификатор события = outbox_events.id. Дублируется в payload намеренно:
  // потребитель не зависит от того, как брокер/коннектор передал заголовки.
  string event_id = 1;

  string order_id = 2;
  string user_id = 3;
  repeated OrderCreatedItem items = 4;
  int64 total_amount = 5;
  string delivery_address = 6;
  google.protobuf.Timestamp created_at = 7;
}

// Позиция заказа на момент создания. Намеренно не переиспользует OrderItem из
// order.proto: событие — публичный контракт для других сервисов и должно
// меняться независимо от gRPC API. Типы полей — те же, что в OrderItem.
message OrderCreatedItem {
  string id = 1;
  string product_id = 2;
  string product_name = 3;
  int32 quantity = 4;
  int64 unit_price = 5;
}
```

Тип позиции у события свой, а не `OrderItem` из API: изменение gRPC-контракта не должно
незаметно менять содержимое топика. Цена — небольшая копипаста в билдере события (раздел 4.4),
зато любое расхождение видно компилятору.

`buf.gen.yaml` не трогаем — новый файл под `proto/proto/` подхватится сам (`task -d proto generate`).

### 3.2 Формат на проводе — protojson, не protobuf-binary

Контракт описан в proto, но **на проводе едет JSON** (`protojson.Marshal`). Причины:

* Debezium Outbox Event Router работает с payload «как есть». Для `jsonb`-колонки +
  `table.expand.json.payload=true` + `JsonConverter` в топик попадает нормальный JSON-объект —
  его видно глазами в Redpanda Console, можно грепать через `rpk`.
* Для protobuf-binary пришлось бы хранить payload в `bytea` и ставить
  `value.converter=io.debezium.converters.BinaryDataConverter` с delegate-конвертером для
  служебных сообщений. Это рабочая схема (см. раздел 12), но отлаживать её вслепую тяжелее,
  а выигрыш по размеру на учебном проекте нулевой.
* Совместимость по эволюции схемы при этом сохраняется: protojson игнорирует неизвестные поля
  при `protojson.UnmarshalOptions{DiscardUnknown: true}`, а правила совместимости proto
  (не переиспользовать номера полей) продолжают действовать.

Имена полей в JSON — snake_case, как в proto и как в JSON gateway. По умолчанию `protojson`
пишет lowerCamelCase (`totalAmount`), поэтому продюсер включает
`protojson.MarshalOptions{UseProtoNames: true}` (раздел 4.4). Консьюмеру ничего настраивать не
нужно: `protojson.Unmarshal` принимает оба варианта имён.

Важная деталь: `protojson` сериализует `int64` как **строку** (`"total_amount": "1500"`).
Это нормально, `protojson.Unmarshal` принимает и строку, и число. Но если кто-то будет читать
топик не через protojson — он должен быть к этому готов.

### 3.3 Топики и версионирование

| Что | Значение |
|---|---|
| Имя топика | `order.events.v1` |
| Формируется как | `route.topic.replacement = ${routedByValue}.events.v1`, где `routedByValue` = `outbox_events.aggregate_type` = `order` |
| Ключ сообщения | `aggregate_id` = `order_id` (UUID-строка) |
| Партиций | 1 (дефолт брокера, см. раздел 5.4) |
| Тип события | заголовок `event-type` (`OrderCreated`) + поле в payload |

Версия `v1` — в имени топика, а не в имени события. Ломающее изменение схемы → новый топик
`order.events.v2`, оба топика живут параллельно, пока потребители не переедут. Неломающие
изменения (новое опциональное поле) — в том же топике.

Один топик на агрегат, а не на тип события: так сохраняется **порядок всех событий одного
заказа** (общий ключ → одна партиция). `OrderCreated`, `OrderPaid`, `OrderCanceled` поедут
в `order.events.v1` и придут потребителю строго в том порядке, в котором легли в WAL.
Потребитель различает их по заголовку `event-type`.

---

## 4. order-service: outbox

### 4.1 Миграция

`services/order/migrations/pg/000002_create_outbox_events_table.up.sql`:

```sql
CREATE TABLE outbox_events (
    id             UUID PRIMARY KEY,
    aggregate_type TEXT        NOT NULL,
    aggregate_id   UUID        NOT NULL,
    type           TEXT        NOT NULL,
    payload        JSONB       NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Для периодической чистки и диагностики по времени.
CREATE INDEX idx_outbox_events_created_at
    ON outbox_events (created_at);
```

`down.sql`:

```sql
DROP TABLE IF EXISTS outbox_events;
```

| Колонка | Смысл |
|---|---|
| `id` | идентификатор события; уезжает в заголовок `id` и дублируется в payload → ключ дедупликации на стороне inbox |
| `aggregate_type` | тип агрегата (`order`); подставляется в имя топика |
| `aggregate_id` | `order.id`; станет ключом Kafka-сообщения → все события одного заказа в одной партиции |
| `type` | `OrderCreated`; уезжает заголовком `event-type` |
| `payload` | тело события (JSON) |
| `created_at` | время создания события; равно `orders.created_at` (билдер берёт `order.CreatedAt`) и передаётся в `INSERT` (`DEFAULT NOW()` в миграции — запасной вариант); в Kafka-сообщение не мапится (timestamp сообщения ставит Debezium при обработке, см. раздел 5.4) |

Имена колонок подобраны так, чтобы быть близко к дефолтам SMT, но в snake_case, как в остальном
проекте. `id` и `payload` совпадают с дефолтами SMT, а дефолты для остальных —
`aggregatetype`/`aggregateid`, поэтому `aggregate_type` и `aggregate_id` в конфиге коннектора
заданы явно (раздел 5.4).

Почему `payload` — `jsonb`, а не `json`: `jsonb` нормализует документ и валидирует его на входе,
битый JSON не доедет до брокера. Цена — потеря порядка ключей, что нам безразлично.

Чего в таблице **нет**: колонок `published_at`, `attempts`, `status`, `updated_at`. Их не должно
быть — это атрибуты поллера, которого у нас нет. Debezium отслеживает позицию в WAL, а не состояние
строк. Строка outbox — неизменяемый факт: вставили один раз и больше не трогаем (append-only).

Почему нет `ALTER TABLE ... REPLICA IDENTITY`. Replica identity определяет, что PostgreSQL
пишет в WAL о старой версии строки при `UPDATE`/`DELETE` (`DEFAULT` — колонки PK, `FULL` — всю
строку). Нужен он самому PostgreSQL: `DELETE` по таблице, которая входит в публикацию с
операцией `delete`, без replica identity падает с `cannot delete from table ... because it does
not have a replica identity and publishes deletes`. Публикация Debezium по умолчанию включает все
операции, но у `outbox_events` есть PK, а `DEFAULT` — это ровно «колонки PK», так что `ALTER`
ничего бы не изменил (проверено: `UPDATE` и `DELETE` по outbox проходят, коннектор остаётся
`RUNNING`). `FULL` не нужен тем более: он раздувает WAL, а Outbox Event Router смотрит только
на INSERT.

### 4.2 Граница транзакции — usecase (Unit of Work)

Сейчас `OrderRepo.Create` сам открывает транзакцию. Её нужно поднять на уровень usecase:
outbox — не забота `OrderRepo`, а решение «что входит в одну бизнес-транзакцию» принимает usecase.

Общий механизм — в `platform/postgres` (там же, где `postgres.New`), новый файл `tx.go`:

```go
package postgres

type txKey struct{}

// Executor — общий интерфейс pgxpool.Pool и pgx.Tx.
type Executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	CopyFrom(ctx context.Context, table pgx.Identifier, columns []string, src pgx.CopyFromSource) (int64, error)
}

// DB возвращает транзакцию из контекста, если она там есть, иначе пул.
// Репозитории всегда ходят в БД через него и не знают, внутри транзакции они или нет.
func DB(ctx context.Context, pool *pgxpool.Pool) Executor {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return pool
}

type TxManager struct {
	pool *pgxpool.Pool
}

func NewTxManager(pool *pgxpool.Pool) *TxManager {
	return &TxManager{pool: pool}
}

func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	// Вложенный вызов переиспользует текущую транзакцию, а не открывает вторую.
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}

	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return pgerr.Map(err)
	}
	defer func() {
		// no-op после успешного Commit. WithoutCancel — чтобы откат прошёл,
		// даже если ctx уже отменён (клиент отвалился, shutdown).
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return pgerr.Map(err)
	}
	return nil
}
```

Репозитории после этого перестают начинать транзакции сами:

```go
func (r *OrderRepo) Create(ctx context.Context, order domain.Order) error {
	db := postgres.DB(ctx, r.pool)

	if err := r.insertOrder(ctx, db, order); err != nil {
		return err
	}
	return r.insertItems(ctx, db, order.Items)
}

func (r *OrderRepo) insertOrder(ctx context.Context, db postgres.Executor, order domain.Order) error {
	if _, err := db.Exec(
		ctx,
		`INSERT INTO orders (id, user_id, status, total_amount, delivery_address, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		order.ID, order.UserID, order.Status, order.TotalAmount, order.DeliveryAddress, order.CreatedAt, order.UpdatedAt,
	); err != nil {
		return pgerr.Map(err)
	}
	return nil
}
```

`insertOrder`/`insertItems` принимают `postgres.Executor` вместо `pgx.Tx`, тело остаётся прежним,
включая `CopyFrom`. Позиции вставляются без времени: у `order_items` нет `created_at`, позиции
неизменяемы и принадлежат заказу, поэтому отдельная отметка повторяла бы `orders.created_at`.

**Время заказа ставит домен.** `NewOrder` берёт один `now := time.Now().UTC()` и записывает его в
`CreatedAt` и `UpdatedAt`. Это время идёт в `INSERT`, в событие и в ответ клиенту, так что везде
оно одно и то же и известно до записи. `DEFAULT NOW()` в миграции остаётся запасным вариантом
(ручная вставка, seed).

### 4.3 OutboxRepo

Отдельный репозиторий, одна таблица — `services/order/internal/repository/pg/outbox.go`:

```go
type OutboxRepo struct {
	pool *pgxpool.Pool
}

func NewOutboxRepo(pool *pgxpool.Pool) *OutboxRepo {
	return &OutboxRepo{pool: pool}
}

func (r *OutboxRepo) Save(ctx context.Context, event domain.OutboxEvent) error {
	if _, err := postgres.DB(ctx, r.pool).Exec(
		ctx,
		`INSERT INTO outbox_events (id, aggregate_type, aggregate_id, type, payload, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)`,
		event.ID, event.AggregateType, event.AggregateID, event.Type, event.Payload, event.CreatedAt,
	); err != nil {
		return pgerr.Map(err)
	}
	return nil
}
```

`event.Payload` — `[]byte` с JSON; pgx положит его в `jsonb` без дополнительных телодвижений.

### 4.4 Домен события

`services/order/internal/domain/outbox.go`:

```go
const AggregateTypeOrder = "order"

const EventTypeOrderCreated = "OrderCreated"

type OutboxEvent struct {
	ID            string
	AggregateType string
	AggregateID   string
	Type          string
	Payload       []byte
	CreatedAt     time.Time
}
```

Домен знает, что событие существует, и не знает, в каком формате оно сериализуется.
Сборка payload — на границе, в `services/order/internal/event/order.go`:

```go
package event

type OrderEventBuilder struct{}

func NewOrderEventBuilder() *OrderEventBuilder { return &OrderEventBuilder{} }

func (b *OrderEventBuilder) OrderCreated(order domain.Order) (domain.OutboxEvent, error) {
	id := uuid.NewV7().String()

	items := make([]*orderv1.OrderCreatedItem, 0, len(order.Items))
	for _, item := range order.Items {
		items = append(items, &orderv1.OrderCreatedItem{
			Id:          item.ID,
			ProductId:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice:   item.UnitPrice,
		})
	}

	// UseProtoNames — snake_case в JSON (order_id, total_amount), как в остальном JSON проекта.
	payload, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(&orderv1.OrderCreated{
		EventId:         id,
		OrderId:         order.ID,
		UserId:          order.UserID,
		Items:           items,
		TotalAmount:     order.TotalAmount,
		DeliveryAddress: order.DeliveryAddress,
		CreatedAt:       timestamppb.New(order.CreatedAt),
	})
	if err != nil {
		return domain.OutboxEvent{}, apperr.Internal().Wrap(err)
	}

	return domain.OutboxEvent{
		ID:            id,
		AggregateType: domain.AggregateTypeOrder,
		AggregateID:   order.ID,
		Type:          domain.EventTypeOrderCreated,
		Payload:       payload,
		CreatedAt:     order.CreatedAt,
	}, nil
}
```

Почему отдельный пакет, а не метод домена: сериализация в protojson тянет зависимость от
сгенерированного `orderv1`, а домен должен остаться чистым. Usecase видит это через интерфейс
(объявленный у потребителя, как `OrderRepo`/`UserProvider` сейчас).

`uuid` здесь — не сторонняя библиотека, а пакет стандартной библиотеки Go (появился в 1.27,
на который уже перешёл проект — см. `services/order/internal/domain/order.go`, тот же `uuid.NewV7()`).
`go.sum` тянет `github.com/google/uuid` транзитивно через другую зависимость — это не тот `uuid`,
что используется в коде, и добавлять его в `go.mod` явно не нужно.

### 4.5 Usecase

```go
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type OrderRepo interface {
	Create(ctx context.Context, order domain.Order) error
}

type OutboxRepo interface {
	Save(ctx context.Context, event domain.OutboxEvent) error
}

type OrderEvents interface {
	OrderCreated(order domain.Order) (domain.OutboxEvent, error)
}

type OrderUsecaseDeps struct {
	Tx       TxManager
	Orders   OrderRepo
	Outbox   OutboxRepo
	Events   OrderEvents
	Users    UserProvider
	Products ProductProvider
}
```

(`Deps` — по значению, раскладывается в поля `OrderUsecase` в конструкторе, как сейчас.)

Хвост `CreateOrder` меняется так:

```go
	order, err := domain.NewOrder(input.UserID, addr.Address, items)
	if err != nil {
		return domain.Order{}, apperr.InvalidArgument().Wrap(err)
	}

	event, err := uc.events.OrderCreated(order)
	if err != nil {
		return domain.Order{}, err
	}

	if err := uc.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := uc.orders.Create(ctx, order); err != nil {
			return err
		}
		return uc.outbox.Save(ctx, event)
	}); err != nil {
		return domain.Order{}, err
	}

	return order, nil
```

Здесь вся суть паттерна: gRPC-вызовы к `user`/`product` — **до** транзакции (нельзя держать
транзакцию открытой на время сетевых вызовов), запись заказа и события — внутри одной.

### 4.6 Сборка в app.go

В `services/order/internal/app/app.go` меняются только блоки «Repository» и «Usecase»;
Postgres, gRPC-клиенты, сервер и `Closer` остаются как есть. Про Kafka `order-service` по-прежнему
ничего не знает.

Порядок важен: `TxManager` и **все** репозитории, которые должны попасть в одну транзакцию,
строятся от одного и того же `pgPool`. Транзакцию репозиторий находит не по пулу, а по `ctx`
(`postgres.DB(ctx, r.pool)`), но пул нужен ему для вызовов вне `WithinTx`.

```go
	// Repository
	txManager := postgres.NewTxManager(pgPool)
	orderRepo := repo.NewOrderRepo(pgPool)
	outboxRepo := repo.NewOutboxRepo(pgPool)

	// Event builder
	orderEvents := event.NewOrderEventBuilder()

	// Provider
	userClient := client.NewUserClient(userConn)
	productClient := client.NewProductClient(productConn)

	// Usecase
	orderUsecase := usecase.NewOrderUsecase(usecase.OrderUsecaseDeps{
		Tx:       txManager,
		Orders:   orderRepo,
		Outbox:   outboxRepo,
		Events:   orderEvents,
		Users:    userClient,
		Products: productClient,
	})
```

Что для этого нужно добавить в импорты:

```go
	"github.com/maksimegorovdev/delivery-backend/services/order/internal/event"
```

`postgres` (`platform/postgres`) уже импортирован — `NewTxManager` лежит рядом с `postgres.New`.

Сам `usecase` `platform/postgres` не импортирует: он видит `TxManager` и `OutboxRepo` только через
интерфейсы из раздела 4.5, а `*postgres.TxManager` подходит под интерфейс неявно — достаточно
метода `WithinTx`. Связывание конкретных типов происходит только здесь, в `app.go`.

Закрывать ничего нового не надо: `TxManager` и репозитории своих ресурсов не держат, пул
закрывается уже зарегистрированным `cl.Add(closer.Wrap(pgPool.Close))`.

### 4.7 Нужно ли чистить outbox

Таблица растёт монотонно. Два подхода:

1. **Хранить N дней** (рекомендуется здесь). Строки остаются, их видно при отладке, всегда можно
   сравнить «что лежит в БД» с «что пришло в топик». Чистка — `DELETE FROM outbox_events WHERE
   created_at < now() - interval '7 days'` раз в сутки (воркер/`pg_cron`). Эти DELETE
   Debezium видит (публикация по умолчанию включает все операции), но `EventRouter` пропускает
   и сам DELETE, и следующий за ним tombstone — в топик ничего не уходит (проверено, раздел 5.4).
2. **`INSERT` + `DELETE` в одной транзакции.** Таблица всегда пустая, но событие в WAL есть и
   Debezium его увидит. Экономит место, но лишает отладочного следа и сильно запутывает при
   первом знакомстве.

Второй вариант — законная оптимизация для прода, но включать его стоит после того, как первый
заработает и будет понятен.

---

## 5. Инфраструктура

Принцип — **минимальный конфиг**. За основу взят пример
[redpanda-labs / cdc / postgres-json](https://github.com/redpanda-data/redpanda-labs/tree/main/docker-compose/cdc/postgres-json)
(Postgres → Debezium → Redpanda), к нему добавлено только то, без чего не работает outbox:
`wal_level=logical` и `EventRouter`. Всё остальное оставлено на дефолтах Debezium и Redpanda, а
всё, что мы сознательно не включили, собрано в таблицу в разделе 5.4 — с пояснением, когда это
возвращать. Так конфиг можно наращивать по одной настройке и понимать, зачем каждая нужна.

Схема из примера — три контейнера: Postgres (`wal_level=logical`), Redpanda, Debezium Connect.
Console к CDC отношения не имеет, она у нас для просмотра топиков.

Минимальная связка проверена в изолированном стенде (2026-09-28: `postgres:18`,
`redpanda:v26.2.3`, `debezium/connect:3.6.3.Final`; конфиги Postgres, Connect и коннектора —
как в этом разделе): коннектор `RUNNING`, строка из `outbox_events` появляется в
`order.events.v1` с ключом, заголовками и JSON-телом, `UPDATE`/`DELETE` по outbox коннектор
не ломают.

### 5.1 PostgreSQL: logical replication

Для CDC нужен `wal_level=logical`. Это единственное изменение в `order-postgres`:

```yaml
  order-postgres:
    container_name: order-postgres
    image: postgres:18
    command: ["postgres", "-c", "wal_level=logical"]
    environment:
      - POSTGRES_USER=postgres
      - POSTGRES_PASSWORD=postgres
      - POSTGRES_DB=orderdb
    ports:
      - "5432:5432"
    volumes:
      - order-postgres-data:/var/lib/postgresql/18/docker
```

`max_wal_senders` и `max_replication_slots` не трогаем: по умолчанию оба равны 10, для одного
коннектора этого с запасом.

Проверка: `docker exec -it order-postgres psql -U postgres -d orderdb -c 'SHOW wal_level;'`
должно вернуть `logical`. Менять `wal_level` можно только рестартом; `docker compose up -d`
пересоздаст контейнер с новой командой.

Пользователь: локально ходим под `postgres` (суперюзер). Это не случайность: по умолчанию
Debezium сам создаёт публикацию `CREATE PUBLICATION dbz_publication FOR ALL TABLES`, а это
может сделать только суперюзер. В проде — отдельный пользователь с `REPLICATION` и
`publication.autocreate.mode=filtered` (таблица в разделе 5.4).

### 5.2 Redpanda + Console

```yaml
  redpanda:
    container_name: redpanda
    image: redpandadata/redpanda:v26.2.3
    command:
      - redpanda
      - start
      - --kafka-addr internal://0.0.0.0:9092,external://0.0.0.0:19092
      - --advertise-kafka-addr internal://redpanda:9092,external://localhost:19092
      - --pandaproxy-addr internal://0.0.0.0:8082,external://0.0.0.0:18082
      - --advertise-pandaproxy-addr internal://redpanda:8082,external://localhost:18082
      - --schema-registry-addr internal://0.0.0.0:8081,external://0.0.0.0:18081
      - --rpc-addr redpanda:33145
      - --advertise-rpc-addr redpanda:33145
      - --mode dev-container
      - --smp 1
      - --default-log-level=info
    ports:
      - "18081:18081"
      - "18082:18082"
      - "19092:19092"
      - "19644:9644"
    volumes:
      - redpanda-data:/var/lib/redpanda/data

  redpanda-console:
    container_name: redpanda-console
    image: redpandadata/console:v3.12.0
    entrypoint: /bin/sh
    command: -c 'echo "$$CONSOLE_CONFIG_FILE" > /tmp/config.yml; /app/console'
    environment:
      CONFIG_FILEPATH: /tmp/config.yml
      CONSOLE_CONFIG_FILE: |
        kafka:
          brokers: ["redpanda:9092"]
        schemaRegistry:
          enabled: true
          urls: ["http://redpanda:8081"]
        redpanda:
          adminApi:
            enabled: true
            urls: ["http://redpanda:9644"]
    ports:
      - "3000:8080"
    depends_on:
      - redpanda
```

Это конфиг из примера redpanda-labs, ничего сверх него.

**`--mode dev-container` — важная строка.** Он включает `auto_create_topics_enabled`: топик
`order.events.v1` создаётся сам при первой записи Debezium (проверено), отдельного шага «создать
топик» нет. В проде режим другой, и топики придётся создавать явно (`rpk topic create`) либо
включать `topic.creation.*` в коннекторе (раздел 5.4).

**Два listener'а — не прихоть.** Сервисы запускаются на хосте (`task dev` → Air), а Debezium —
в docker. Поэтому:

* `debezium` → `redpanda:9092` (internal);
* `notification-service` с хоста → `localhost:19092` (external).

Если объявить только один listener с `advertise-kafka-addr redpanda:9092`, клиент с хоста
подключится к брокеру, получит в metadata адрес `redpanda:9092` и отвалится по DNS.

Console опубликована на `localhost:3000` (внутри контейнера — `8080`), так что порт `8080`
остаётся свободным для сервисов. Подключение Connect к Console в минимальный конфиг не входит
(таблица в разделе 5.4).

### 5.3 Kafka Connect с Debezium

Сервис называется `debezium`, как в примере redpanda-labs: это Kafka Connect с плагинами
Debezium, а имя контейнера подсказывает, зачем он нужен.

```yaml
  debezium:
    container_name: debezium
    image: quay.io/debezium/connect:3.6.3.Final
    environment:
      BOOTSTRAP_SERVERS: redpanda:9092
      GROUP_ID: delivery-connect
      CONFIG_STORAGE_TOPIC: connect_configs
      OFFSET_STORAGE_TOPIC: connect_offsets
    ports:
      - "8083:8083"
    depends_on:
      - order-postgres
      - redpanda
```

Обязательны только `BOOTSTRAP_SERVERS`, `GROUP_ID` и два служебных топика — как в примере
redpanda-labs. Больше ничего не нужно (проверено на этой связке):

* **`*_STORAGE_REPLICATION_FACTOR` не нужны.** Служебные топики создаются с 1 репликой и без
  них. Раньше здесь утверждалось обратное («без `=1` коннект не стартует») — на одном брокере
  Redpanda это не подтвердилось.
* **`STATUS_STORAGE_TOPIC` не задан.** Образ печатает `WARNING: it is recommended to specify
  the STATUS_STORAGE_TOPIC` и использует имя по умолчанию `connect-status` (5 партиций).
* **Конвертеры на уровне воркера не заданы** — они прописаны в самом коннекторе.

`depends_on` задаёт только порядок старта, без ожидания готовности брокера. Если `debezium`
стартовал раньше Redpanda и упал — поднять его снова.

### 5.4 Конфигурация коннектора

Кладём в `deploy/debezium/order-outbox.json` (только объект `config`, чтобы регистрировать
идемпотентным `PUT /connectors/{name}/config`):

```json
{
  "connector.class": "io.debezium.connector.postgresql.PostgresConnector",
  "plugin.name": "pgoutput",

  "database.hostname": "order-postgres",
  "database.port": "5432",
  "database.user": "postgres",
  "database.password": "postgres",
  "database.dbname": "orderdb",

  "topic.prefix": "order-service",
  "table.include.list": "public.outbox_events",

  "transforms": "outbox",
  "transforms.outbox.type": "io.debezium.transforms.outbox.EventRouter",
  "transforms.outbox.table.field.event.key": "aggregate_id",
  "transforms.outbox.table.fields.additional.placement": "type:header:event-type",
  "transforms.outbox.table.expand.json.payload": "true",
  "transforms.outbox.route.by.field": "aggregate_type",
  "transforms.outbox.route.topic.replacement": "${routedByValue}.events.v1",

  "key.converter": "org.apache.kafka.connect.storage.StringConverter",
  "value.converter": "org.apache.kafka.connect.json.JsonConverter",
  "value.converter.schemas.enable": "false"
}
```

Первые две группы — то же, что в примере redpanda-labs (`connector.class`, `plugin.name`,
`database.*`, `topic.prefix`, `table.include.list`). Остальное — то, без чего outbox не
заработает. Построчно:

* **`plugin.name=pgoutput`** — задать явно. Дефолт коннектора до сих пор `decoderbufs`, а это
  внешнее расширение, которого в образе `postgres:18` нет. `pgoutput` встроен в PostgreSQL.
* **`topic.prefix`** — обязателен, но в топики событий не попадает: имя формирует SMT. Проверено:
  «сырого» топика `orderdb.public.outbox_events` вообще не создаётся, `EventRouter` отправляет
  запись сразу в `order.events.v1`.
* **`table.include.list`** — только outbox-таблица.
* **`transforms.outbox.*`** — Outbox Event Router. Заданы только те его настройки, у которых
  дефолт не подходит:
  * `table.field.event.key=aggregate_id` — дефолт `aggregateid`;
  * `route.by.field=aggregate_type` — дефолт `aggregatetype`;
  * `route.topic.replacement=${routedByValue}.events.v1` — дефолт `outbox.event.${routedByValue}`;
  * `table.fields.additional.placement=type:header:event-type` — единственный способ вынести
    колонку `type` наружу (опции вида `table.field.event.type` не существует); без неё SMT кладёт
    в заголовки только `id`;
  * `table.expand.json.payload=true` — без него значение сообщения будет JSON-**строкой** с
    экранированными кавычками, и потребителю пришлось бы сначала разэкранировать её, а потом
    парсить. С ним в топике лежит нормальный объект.

  Совпадающие с дефолтами `id` (уезжает заголовком `id`) и `payload` (становится телом
  сообщения) не перечислены.
* **`key.converter` / `value.converter`** — ключ строкой (иначе он уедет JSON-представлением, то
  есть UUID в кавычках), значение — JSON без конверта `schema`/`payload`
  (`schemas.enable=false`). Заданы в коннекторе, а не в env воркера: конфиг самодостаточен.

**Что Debezium и Redpanda создают сами** (дефолты, проверено):

| Что | Значение |
|---|---|
| Слот репликации | `debezium` (плагин `pgoutput`) |
| Публикация | `dbz_publication`, `FOR ALL TABLES`, все операции (`insert`/`update`/`delete`/`truncate`) |
| Топик событий | `order.events.v1`: 1 партиция, 1 реплика (создаёт Redpanda при первой записи) |
| Служебные топики Connect | `connect_configs`, `connect_offsets`, `connect-status` |

**Как выглядит сообщение в топике** (проверено на тестовой строке):

```
key     = aggregate_id                                   # строка
headers = id=<outbox_events.id>; event-type=OrderCreated; __debezium.context.*=…
value   = {"event_id":"…","order_id":"…","total_amount":"1500"}   # payload как есть
```

Заголовки `__debezium.context.*` — служебные, их игнорируем.

**UPDATE и DELETE по outbox** (проверено): коннектор остаётся `RUNNING`, в топике остаётся ровно
одно сообщение. `EventRouter` пропускает DELETE и tombstone молча, а на UPDATE пишет
предупреждение в лог (`table.op.invalid.behavior=warn`) и тоже ничего не публикует.

#### Что не включено в минимальный конфиг — и когда добавлять

| Настройка | Что даёт | Когда добавить |
|---|---|---|
| `snapshot.mode=no_data` | не переотправлять строки, уже лежащие в outbox, при первом старте | по умолчанию `initial`: `EventRouter` пропускает только DELETE и UPDATE, так что снапшотные записи он **отправит**. Дубли отсечёт inbox; если переотправка не нужна — включить. Это то, что в Debezium 2.x называлось `never` |
| `publication.autocreate.mode=filtered`, `publication.name`, `slot.name` | публикация только на таблицы из `table.include.list`, свои имена слота и публикации | прод, пользователь без суперюзера, второй коннектор к той же БД. С `filtered` Debezium делает `CREATE PUBLICATION ... FOR TABLE`, а для этого нужно владеть таблицами. Альтернатива: публикация в миграции и `autocreate.mode=disabled` |
| `skipped.operations=u,d,t` | вместе с `filtered` публикация создаётся с `publish = 'insert'`: PostgreSQL вообще не шлёт UPDATE/DELETE/TRUNCATE в слот | когда важен объём трафика по слоту. Для корректности не нужно: `EventRouter` и так игнорирует DELETE/UPDATE |
| `tombstones.on.delete=false` | не слать tombstone после DELETE | не нужно: `EventRouter` его отбрасывает (проверено) |
| `topic.creation.enable`, `topic.creation.default.partitions`, `.replication.factor`, `.retention.ms` | параметры создаваемого топика | сейчас топик создаёт Redpanda: 1 партиция. Нужны несколько консьюмеров в одной группе — задать здесь или создать топик заранее: `rpk topic create order.events.v1 -p 3`. В проде без автосоздания — обязательно |
| heartbeat (см. ниже) | слот не залипает, WAL не пухнет | когда в БД активно пишут в другие таблицы, а outbox подолгу молчит |
| `transforms.outbox.table.field.event.timestamp=created_at` | timestamp Kafka-сообщения = время создания события, а не время обработки Debezium | нужен точный event-time в Kafka |
| `aggregate_type:header:aggregate-type` в `additional.placement` | тип агрегата отдельным заголовком | консьюмеру нужен тип без разбора имени топика |
| `errors.log.enable`, `errors.log.include.messages` | подробный лог ошибок обработки (SMT, конвертеры) вместе с сообщением | отладка падений `EventRouter` |
| `STATUS_STORAGE_TOPIC` (в Connect) | явное имя топика статусов, нет WARNING | если мешает WARNING или нужна единая схема имён |
| `*_STORAGE_REPLICATION_FACTOR` (в Connect) | replication factor служебных топиков | кластер из нескольких брокеров; на одном не нужно |
| Connect в Console | видеть коннектор и его статус в UI | по желанию; настройка в конфиге Console (секция для Kafka Connect) — синтаксис сверить с документацией версии `v3.12.0` |

##### Heartbeat: когда понадобится

Слот репликации держит WAL до последнего подтверждённого LSN, а Debezium подтверждает его,
только когда видит событие из своих таблиц. Если в `orderdb` идёт активность (записи в `orders`),
а в `outbox_events` какое-то время тишина, WAL пухнет на диске. Лечит heartbeat: раз в интервал
Debezium сам пишет строку в служебную таблицу, получает её из WAL и подтверждает свежий LSN.

В конфиг коннектора добавляется:

```json
  "table.include.list": "public.outbox_events,public.debezium_heartbeat",
  "heartbeat.interval.ms": "10000",
  "heartbeat.action.query": "INSERT INTO debezium_heartbeat DEFAULT VALUES",

  "predicates": "isOutbox",
  "predicates.isOutbox.type": "org.apache.kafka.connect.transforms.predicates.TopicNameMatches",
  "predicates.isOutbox.pattern": "orderdb\\.public\\.outbox_events",
  "transforms.outbox.predicate": "isOutbox"
```

и миграция в order-сервисе:

```sql
CREATE TABLE debezium_heartbeat (
    id BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    ts TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

* heartbeat-таблица должна быть в `table.include.list` — иначе Debezium не увидит собственную
  запись и LSN не сдвинется;
* `predicate` нужен, чтобы `EventRouter` не применялся к событиям этой таблицы: у них другая
  структура, у них нет колонок outbox;
* если включена insert-only публикация (`skipped.operations`), запрос должен быть именно
  `INSERT`: `UPDATE` (`ON CONFLICT DO UPDATE`) Debezium не увидит;
* таблица растёт на строку за heartbeat (~8,6 тыс. в сутки при интервале 10 с) — чистить тем же
  заданием, что и outbox: `DELETE FROM debezium_heartbeat WHERE ts < now() - interval '1 day'`.

### 5.5 Регистрация коннектора

```bash
curl -sS -X PUT \
  -H 'Content-Type: application/json' \
  --data @deploy/debezium/order-outbox.json \
  http://localhost:8083/connectors/order-outbox/config
```

`PUT` на `/config` идемпотентен: первый вызов создаёт коннектор, последующие обновляют.
Оформляем задачей в корневом `Taskfile.yml` (раздел 8).

---

## 6. notification-service: consumer + inbox

### 6.1 Своя БД

У сервиса сейчас есть `pgPool`, но нет ни БД в compose, ни миграций. Заводим:

```yaml
  notification-postgres:
    container_name: notification-postgres
    image: postgres:18
    environment:
      - POSTGRES_USER=postgres
      - POSTGRES_PASSWORD=postgres
      - POSTGRES_DB=notificationdb
    ports:
      - "5435:5432"
    volumes:
      - notification-postgres-data:/var/lib/postgresql/18/docker
```

В `services/notification/.env.example` сейчас `PG_DSN` указывает на `localhost:5434/userdb` —
это чужая БД (5434 = productdb). Исправить на:

```env
PG_DSN=postgresql://postgres:postgres@localhost:5435/notificationdb?sslmode=disable
PG_MAX_CONNS=10
# Kafka
KAFKA_BROKERS=localhost:19092
KAFKA_CONSUMER_GROUP=notification
KAFKA_TOPICS=order.events.v1
```

Под эти переменные в `services/notification/internal/config/config.go` нужен блок `Kafka` — ровно
в том же стиле, что `PG`/`GRPCServer` в order-сервисе (структура + `Validate()` + регистрация
в `Config.Validate()`), а не голый парсинг строк по месту:

```go
type Config struct {
	App   App
	Log   Log
	PG    PG
	Kafka Kafka
}

func (c Config) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.App, validation.Required),
		validation.Field(&c.Log, validation.Required),
		validation.Field(&c.PG, validation.Required),
		validation.Field(&c.Kafka, validation.Required),
	)
}

type Kafka struct {
	Brokers       []string `env:"KAFKA_BROKERS,required"`
	ConsumerGroup string   `env:"KAFKA_CONSUMER_GROUP,required"`
	Topics        []string `env:"KAFKA_TOPICS,required"`
}

func (k Kafka) Validate() error {
	return validation.ValidateStruct(&k,
		validation.Field(&k.Brokers, validation.Required),
		validation.Field(&k.ConsumerGroup, validation.Required),
		validation.Field(&k.Topics, validation.Required),
	)
}
```

`[]string` для `KAFKA_BROKERS`/`KAFKA_TOPICS` разбирается `caarlos0/env` по запятой без
дополнительных тегов — сепаратор по умолчанию как раз `,`, отдельный `envSeparator` не нужен.

### 6.2 Миграции inbox и notifications

`services/notification/migrations/pg/000001_create_inbox_events_table.up.sql`:

```sql
CREATE TABLE inbox_events (
    event_id     UUID PRIMARY KEY,
    topic        TEXT        NOT NULL,
    partition    INT         NOT NULL,
    msg_offset   BIGINT      NOT NULL,
    event_type   TEXT        NOT NULL,
    aggregate_id UUID        NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_inbox_events_received_at
    ON inbox_events (received_at);
```

`event_id` — первичный ключ, и это весь механизм дедупликации. `topic/partition/msg_offset`
хранятся для диагностики («откуда приехало»), уникальности по ним не требуем: после ребаланса
одно и то же событие может прийти с тем же оффсетом повторно — его отсечёт PK.

`offset` — зарезервированное слово в SQL, поэтому колонка называется `msg_offset`.

**`payload` в inbox не храним:** это журнал дедупликации, тело события ему не нужно. Тело лежит
в `notifications.payload` (там оно нужно воркеру) и в Redpanda/`outbox_events` order-сервиса.

`services/notification/migrations/pg/000002_create_notifications_table.up.sql`:

```sql
CREATE TABLE notifications (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id   UUID        NOT NULL,
    event_type TEXT        NOT NULL,
    channel    TEXT        NOT NULL,
    payload    JSONB       NOT NULL,
    status     TEXT        NOT NULL DEFAULT 'pending',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- состояние доставки (раздел 6.6)
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    locked_until    TIMESTAMPTZ,
    last_error      TEXT,
    sent_at         TIMESTAMPTZ,

    CONSTRAINT uq_notifications_event_channel UNIQUE (event_id, channel),
    CONSTRAINT chk_notifications_channel CHECK (channel IN ('log')),
    CONSTRAINT chk_notifications_status CHECK (status IN ('pending', 'sent', 'failed'))
);

CREATE INDEX idx_notifications_pending_next_attempt_at
    ON notifications (next_attempt_at) WHERE status = 'pending';
```

`down.sql`: `DROP TABLE notifications;`.

`inbox_events` и `notifications` — разные сущности, поэтому две таблицы. `inbox_events` — журнал
дедупликации: короткая жизнь (окно повторов, например 7 дней), insert-only, PK `event_id`.
`notifications` — данные сервиса: долгая жизнь, меняется статус. Слияние ломает чистку inbox,
не выражает «одно событие — несколько каналов» и смешивает UPDATE воркера со вставками `Claim`.
Связь логическая: `event_id` копируется, FK на `inbox_events` **не ставим** — inbox чистится.
`event_type` нужен воркеру, чтобы знать, как разбирать `payload`: события разных типов приходят
с разным телом, а `JSONB` позволяет держать их в одной таблице без новых колонок на каждое.
`UNIQUE (event_id, channel)` — вторая линия защиты от дублей, если inbox почистили раньше времени.
Статусов три: `pending` (ждёт отправки или повтора), `sent` и `failed` (исчерпаны попытки либо
ошибка заведомо постоянная). Допустимые значения закреплены `CHECK` (опечатка в статусе иначе
молча выведет запись из выборки воркера; CHECK, а не enum — новый статус добавляется
пересозданием `chk_notifications_status`, без `ALTER TYPE`). Пока единственный `channel` — `log`, он же закреплён `CHECK`; с каждым новым каналом
(`email`, `push`) `chk_notifications_channel` пересоздаётся в той же миграции, что добавляет канал.

**Поля доставки** (подробно — раздел 6.6):

| Колонка | Смысл |
|---|---|
| `attempts` | сколько раз запись захватывали на отправку; растёт при захвате, а не при ошибке |
| `next_attempt_at` | не раньше какого времени пробовать снова (экспоненциальная пауза после ошибки) |
| `locked_until` | «аренда»: пока не истекла, запись обрабатывает другой воркер; упавший воркер её не снимает — она просто истекает |
| `last_error` | текст последней ошибки отправки |
| `sent_at` | момент успешной отправки |

**Частичный индекс `idx_notifications_pending_next_attempt_at`.** Содержит только строки со `status = 'pending'`: как
только запись становится `sent` или `failed`, она выпадает из индекса. Поэтому он остаётся
маленьким, сколько бы отправленных уведомлений ни накопилось, и запрос воркера не замедляется с
ростом таблицы. Индекс подойдёт запросу, только если в нём буквально `status = 'pending'` (не
`IN (...)` и не параметр).

**Ни получателя, ни `user_id` в таблице пока нет:** канал `log` в них не нуждается. Когда
появится email, отдельной миграцией добавятся `user_id` и `recipient` (раздел 6.6).

### 6.3 platform/kafka/kafkaconsumer

Файлы пакета (как у `platform/grpc/grpcserver`):

```
platform/kafka/kafkaconsumer/
    kafkaconsumer.go   # Handler, ErrorHandler, Consumer, New, Run, Close
    options.go         # Option, WithErrorHandler, WithMaxPollRecords, WithRetryBackoff, WithClientOptions
```

По конвенции проекта (`platform/grpc/grpcserver`, `platform/http/httpserver`) — тонкая обёртка
жизненного цикла с функциональными опциями. Пакет называем по протоколу (`kafka`), а не по
вендору (`redpanda`): протокол Kafka-совместимый, брокер заменяем. Что бы ни стояло за адресом
брокера — Redpanda, сама Kafka, WarpStream — код консьюмера остаётся тем же.

Клиент — `github.com/twmb/franz-go`: чистый Go без cgo (в отличие от `confluent-kafka-go`,
который тянет `librdkafka` и ломает кросс-компиляцию), consumer group'ы из коробки,
в апстриме тестируется в том числе против Redpanda.

Как и остальные пакеты `platform/`, консьюмер **не логирует**. Ошибки обработки возвращает
`Handler`, и логирует их сам сервис: только у него есть контекст (`event_id`, тип события).
Нефатальные ошибки commit пакет отдаёт через хук `ErrorHandler`, а что с ними делать —
логировать, считать в метриках — решает сервис в `app.go`. Ошибки fetch фатальны: `Run`
возвращает их, и сервис завершается.

```go
package kafkaconsumer

const (
	defaultMaxPollRecords = 100
	defaultRetryBackoff   = time.Second
)

// Handler получает запись как есть, *kgo.Record: по конвенции platform/ (как pgxpool в postgres
// и grpc.Server в grpcserver) типы библиотеки не прячутся. Важно, чтобы kgo не уходил глубже
// транспортного слоя: usecase и домен о нём не знают.
type Handler interface {
	Handle(ctx context.Context, rec *kgo.Record) error
}

// ErrorHandler получает нефатальные ошибки commit (худшее последствие — повторная доставка,
// её отсекает inbox). Run из-за них не завершается. Ошибки Handle сюда не попадают: их логирует
// хендлер. Ошибки fetch тоже: они неретраибельны, Run возвращает их и завершается.
type ErrorHandler func(ctx context.Context, err error)

type Consumer struct {
	client         *kgo.Client
	onError        ErrorHandler
	maxPollRecords int
	retryBackoff   time.Duration
	clientOpts     []kgo.Opt
}

func New(brokers []string, group string, topics []string, opts ...Option) (*Consumer, error) {
	c := &Consumer{
		onError:        func(context.Context, error) {},
		maxPollRecords: defaultMaxPollRecords,
		retryBackoff:   defaultRetryBackoff,
	}
	for _, opt := range opts {
		opt(c)
	}

	// Пользовательские опции (WithClientOptions) идут первыми, служебные — последними: так
	// сервис не сможет перекрыть то, на чём держится Run (ручной коммит, блокировка ребаланса).
	clientOpts := append(c.clientOpts,
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		// Оффсеты коммитим руками — только после успешной обработки.
		kgo.DisableAutoCommit(),
		// Ребаланс не должен случиться, пока батч в обработке.
		kgo.BlockRebalanceOnPoll(),
	)

	client, err := kgo.NewClient(clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("kafkaconsumer: client: %w", err)
	}

	c.client = client
	return c, nil
}

func (c *Consumer) Run(ctx context.Context, h Handler) error {
	defer c.client.AllowRebalance()

	for {
		fetches := c.client.PollRecords(ctx, c.maxPollRecords)
		if ctx.Err() != nil || fetches.IsClientClosed() {
			return nil
		}

		// franz-go ретраит всё ретраибельное сам, до poll доходят только неретраибельные
		// ошибки (нет прав на топик/группу и т. п.). Продолжать бессмысленно: возвращаем
		// ошибку, errgroup остановит сервис, перезапуск — дело оркестратора.
		var fetchErrs []error
		fetches.EachError(func(t string, p int32, err error) {
			fetchErrs = append(fetchErrs, fmt.Errorf("topic=%s partition=%d: %w", t, p, err))
		})
		if len(fetchErrs) > 0 {
			return fmt.Errorf("kafkaconsumer: fetch: %w", errors.Join(fetchErrs...))
		}

		var (
			processed []*kgo.Record
			rewind    = make(map[string]map[int32]kgo.EpochOffset)
		)

		// Партиции независимы, но внутри партиции строго по порядку.
		fetches.EachPartition(func(p kgo.FetchTopicPartition) {
			for _, rec := range p.Records {
				// Ошибку Handle не логируем: это делает сам хендлер, у него есть контекст
				// (тип события и т. п.).
				if err := h.Handle(ctx, rec); err != nil {
					// Дальше по этой партиции не идём: порядок важнее пропускной способности.
					// Запоминаем, куда откатить позицию чтения.
					if rewind[rec.Topic] == nil {
						rewind[rec.Topic] = make(map[int32]kgo.EpochOffset)
					}
					rewind[rec.Topic][rec.Partition] = kgo.EpochOffset{
						Epoch:  rec.LeaderEpoch,
						Offset: rec.Offset,
					}
					return
				}
				processed = append(processed, rec)
			}
		})

		if len(processed) > 0 {
			// WithoutCancel: при остановке (ctx отменён) уже обработанное всё равно коммитим.
			if err := c.client.CommitRecords(context.WithoutCancel(ctx), processed...); err != nil {
				c.onError(ctx, fmt.Errorf("kafkaconsumer: commit: %w", err))
			}
		}

		// Позиция чтения клиента уже ушла за весь батч. Без отката следующий poll продолжит
		// со следующей записи, и упавшая будет пропущена. SetOffsets — после коммита и до
		// AllowRebalance: так требует документация franz-go для group-консьюмера.
		if len(rewind) > 0 {
			c.client.SetOffsets(rewind)
		}

		c.client.AllowRebalance()

		if len(rewind) > 0 {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(c.retryBackoff):
			}
		}
	}
}

// CloseAllowingRebalance: при BlockRebalanceOnPoll обычный Close может зависнуть на ребалансе.
func (c *Consumer) Close() { c.client.CloseAllowingRebalance() }
```

Опции — `platform/kafka/kafkaconsumer/options.go`:

```go
type Option func(*Consumer)

func WithErrorHandler(h ErrorHandler) Option {
	return func(c *Consumer) { c.onError = h }
}

func WithMaxPollRecords(n int) Option {
	return func(c *Consumer) { c.maxPollRecords = n }
}

func WithRetryBackoff(d time.Duration) Option {
	return func(c *Consumer) { c.retryBackoff = d }
}

// WithClientOptions — запасной выход к настройкам franz-go, для которых нет своей опции.
func WithClientOptions(opts ...kgo.Opt) Option {
	return func(c *Consumer) { c.clientOpts = append(c.clientOpts, opts...) }
}
```

**`WithClientOptions`.** Через неё сервис передаёт любые `kgo.Opt`, не заводя отдельную опцию
на каждую настройку клиента. Типичные случаи:

* логи самого franz-go (ребалансы, переподключения):
  `kgo.WithLogger(kslog.New(log))` (`github.com/twmb/franz-go/plugin/kslog`). Логгер приходит из
  сервиса, пакет о нём не знает;
* `kgo.SessionTimeout`, `kgo.InstanceID` (статическое членство — меньше ребалансов при рестарте);
* TLS и SASL для прода, хуки для метрик и трассировки.

Служебные опции (`DisableAutoCommit`, `BlockRebalanceOnPoll` и т. д.) в `New` добавляются
**после** пользовательских, поэтому перекрыть их нельзя: на них держится логика `Run`.

Четыре принципиальных решения в этом коде:

1. **`DisableAutoCommit` + ручной `CommitRecords` после обработки.** Автокоммит сдвигает оффсет
   по таймеру, независимо от того, обработано сообщение или нет, — это превращает at-least-once
   в at-most-once (потеря при падении).
2. **`BlockRebalanceOnPoll` + `AllowRebalance`.** Без этого партиция может уехать к другому
   консьюмеру прямо во время обработки батча, и `CommitRecords` для чужой партиции не пройдёт.
3. **Остановка на первой ошибке в партиции + откат через `SetOffsets`.** Пропустить сообщение
   и пойти дальше — значит нарушить порядок и потерять событие. Просто не коммитить
   **недостаточно**: незакоммиченный оффсет влияет только на рестарт и ребаланс, а позиция
   чтения клиента сдвигается сразу, как только `PollRecords` вернул записи. Поэтому позицию
   партиции явно возвращаем на упавшую запись — на следующем poll она приедет снова. Это и
   есть ретрай.
4. **Пауза `retryBackoff` после неудачного батча.** Без неё консьюмер крутит упавшее сообщение
   в плотном цикле. Пауза общая для всех партиций — для одного консьюмера это приемлемо.
   Ждём уже после `AllowRebalance`, чтобы не держать ребаланс на время сна.

Зацикливание на «ядовитом» сообщении (битый JSON, которое никогда не обработается) всё равно
остаётся реальным риском: backoff лишь замедляет цикл. Минимальная защита: счётчик попыток
в памяти, после N неудач — запись в DLQ-топик (`order.events.v1.dlq`) и коммит. Это следующий
шаг, не первый.

### 6.4 Repository / usecase / transport

Структура повторяет order-сервис:

```
services/notification/internal/
    app/app.go
    config/config.go
    domain/
        event.go              # domain.OrderCreated, EventTypeOrderCreated — своя модель, не proto
        inbox.go              # domain.InboxEvent
        notification.go       # domain.Notification
    notify/
        log.go                # LogNotifier (реализация usecase.Sender, канал log)
    repository/pg/
        inbox.go              # InboxRepo, Claim
        notification.go       # NotificationRepo: Create, ClaimBatch, MarkSent, MarkRetry, MarkFailed
    transport/kafka/
        router.go             # OrderRouter, OrderRouterDeps, Handle/route, headerEventType
        order.go              # handleOrderCreated: декод protojson → usecase input
    usecase/
        notification.go       # интерфейсы, NotificationUsecase, HandleOrderCreatedInput
        sender.go             # SenderUsecase (воркер отправки, раздел 6.6)
    worker/
        sender.go             # SenderWorker: тикер → SenderUsecase.RunOnce
```

Конфиг (`Config.Kafka`) — в `internal/config/config.go`, сборка — в `internal/app/app.go`.
`kafkaconsumer` — из `platform/`, не из сервиса.

**Repository — `Claim`, а не `Insert` + `Exists`.**
`services/notification/internal/repository/pg/inbox.go`, доменная модель —
`internal/domain/inbox.go`:

```go
type InboxEvent struct {
	EventID     string
	Topic       string
	Partition   int32
	Offset      int64
	EventType   string
	AggregateID string
}
```

```go
type InboxRepo struct {
	pool *pgxpool.Pool
}

func NewInboxRepo(pool *pgxpool.Pool) *InboxRepo { return &InboxRepo{pool: pool} }

func (r *InboxRepo) Claim(ctx context.Context, event domain.InboxEvent) (bool, error) {
	tag, err := postgres.DB(ctx, r.pool).Exec(
		ctx,
		`INSERT INTO inbox_events (event_id, topic, partition, msg_offset, event_type, aggregate_id)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (event_id) DO NOTHING`,
		event.EventID, event.Topic, event.Partition, event.Offset, event.EventType, event.AggregateID,
	)
	if err != nil {
		return false, pgerr.Map(err)
	}
	return tag.RowsAffected() == 1, nil
}
```

`Claim` пытается застолбить сообщение. `true` — сообщение занято впервые, `false` — дубликат
(`ON CONFLICT DO NOTHING` не вставил строку, `RowsAffected` равен нулю). `received_at` ставит БД
(`DEFAULT NOW()`), коду оно не нужно, поэтому в `InboxEvent` его нет.

Проверка `SELECT ... WHERE event_id = $1` отдельным запросом — гонка: два консьюмера (или один
после ребаланса) могут пройти проверку одновременно. `INSERT ... ON CONFLICT DO NOTHING` +
`RowsAffected` атомарен.

**Usecase — Unit of Work, как в order:**

Файл `services/notification/internal/usecase/notification.go`. Интерфейсы объявлены у
потребителя (как `OrderRepo`/`UserProvider` в order-сервисе), `Deps` — по значению,
раскладывается в поля в конструкторе (см. раздел 4.5). Доменная модель события —
`internal/domain/event.go`:

```go
const EventTypeOrderCreated = "OrderCreated"

type OrderCreated struct {
	EventID         string
	OrderID         string
	UserID          string
	Items           []OrderItem
	TotalAmount     int64
	DeliveryAddress string
	CreatedAt       time.Time
}

type OrderItem struct {
	ID          string
	ProductID   string
	ProductName string
	Quantity    int32
	UnitPrice   int64
}
```

```go
// HandleOrderCreatedInput — вход usecase: «конверт» для inbox, сырое тело (для notifications)
// и разобранное событие.
type HandleOrderCreatedInput struct {
	Message domain.InboxEvent
	Payload []byte
	Event   domain.OrderCreated
}

type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type InboxRepo interface {
	Claim(ctx context.Context, event domain.InboxEvent) (bool, error)
}

type NotificationRepo interface {
	Create(ctx context.Context, n domain.Notification) error
}

type NotificationUsecaseDeps struct {
	Tx            TxManager
	Inbox         InboxRepo
	Notifications NotificationRepo
	Log           *slog.Logger
}

type NotificationUsecase struct {
	tx            TxManager
	inbox         InboxRepo
	notifications NotificationRepo
	log           *slog.Logger
}

func NewNotificationUsecase(deps NotificationUsecaseDeps) *NotificationUsecase {
	return &NotificationUsecase{
		tx:            deps.Tx,
		inbox:         deps.Inbox,
		notifications: deps.Notifications,
		log:           deps.Log,
	}
}
```

```go
func (uc *NotificationUsecase) HandleOrderCreated(
	ctx context.Context,
	input HandleOrderCreatedInput,
) error {
	return uc.tx.WithinTx(ctx, func(ctx context.Context) error {
		claimed, err := uc.inbox.Claim(ctx, input.Message)
		if err != nil {
			return err
		}
		if !claimed {
			uc.log.Debug("duplicate event skipped",
				slog.String("event_id", input.Message.EventID))
			return nil
		}

		return uc.notifications.Create(ctx, domain.Notification{
			EventID:   input.Message.EventID,
			EventType: input.Message.EventType,
			Channel:   domain.ChannelLog, // позже: по строке на каждый канал
			Payload:   input.Payload,
		})
	})
}
```

`NotificationRepo.Create` — `INSERT INTO notifications (event_id, event_type, channel, payload)
VALUES ($1, $2, $3, $4) ON CONFLICT (event_id, channel) DO NOTHING`. `domain.Notification`
(`internal/domain/notification.go`) — поля `ID, EventID, EventType, Channel, Payload` (теги
`db:"..."` под `pgx.RowToStructByName`), `ChannelLog = "log"`.

Дедупликация и запись `notifications` — в одной транзакции: либо есть обе строки, либо ни одной.
Если упадёт `Create`, откатится и запись в inbox, оффсет не закоммитится, сообщение приедет
снова. Внешнего эффекта в транзакции приёма нет — его делает воркер (раздел 6.6).

**Эффект вне БД — не в транзакции приёма.** Печать в лог (как и отправка письма) не участвует
в транзакции — на неё транзакционных гарантий нет по определению. Поэтому приём лишь фиксирует
намерение (`notifications.status = pending`), а отправку делает воркер (раздел 6.6): упал между
`Send` и `MarkSent` — отправка повторится («хотя бы раз»). Для реальных каналов (email/push)
повтор гасится идемпотентным ключом на стороне провайдера (`notifications.id`).

**Inbox растёт монотонно — как и outbox (раздел 4.7).** Хранить окно возможных повторов
(например, 7 дней) и чистить раз в сутки:
`DELETE FROM inbox_events WHERE received_at < now() - interval '7 days'` — для этого и нужен
индекс `idx_inbox_events_received_at`. Повтор старше окна (например, после `rpk group seek` на
начало) снова пройдёт `Claim` — это осознанный компромисс, сейчас чистку не делаем (раздел 12).

**Transport — маршрутизация по типу события.**
`services/notification/internal/transport/kafka/router.go` и `order.go`. Пакет называется
`kafka`, в `app.go` импортируется с алиасом `kafkarouter`. Usecase объявлен интерфейсом у
потребителя:

```go
const headerEventType = "event-type"

type NotificationUsecase interface {
	HandleOrderCreated(ctx context.Context, in usecase.HandleOrderCreatedInput) error
}

type OrderRouterDeps struct {
	NotificationUsecase NotificationUsecase
	Log                 *slog.Logger
}

type OrderRouter struct {
	notification NotificationUsecase
	log          *slog.Logger
}

func NewOrderRouter(deps OrderRouterDeps) *OrderRouter {
	return &OrderRouter{notification: deps.NotificationUsecase, log: deps.Log}
}
```

```go
// Handle — точка входа от консьюмера. Ошибку логируем здесь: консьюмер из platform/ не
// логирует, а контекст сообщения (тип события, откуда приехало) есть только у нас.
func (r *OrderRouter) Handle(ctx context.Context, rec *kgo.Record) error {
	eventType := header(rec, headerEventType)

	if err := r.route(ctx, rec, eventType); err != nil {
		r.log.ErrorContext(ctx, "handle message",
			slog.String("topic", rec.Topic),
			slog.Int("partition", int(rec.Partition)),
			slog.Int64("offset", rec.Offset),
			slog.String("event_type", eventType),
			logger.Err(err))
		return err
	}
	return nil
}

func (r *OrderRouter) route(ctx context.Context, rec *kgo.Record, eventType string) error {
	switch eventType {
	case domain.EventTypeOrderCreated:
		return r.handleOrderCreated(ctx, rec)
	default:
		// Неизвестный тип — не ошибка: старый консьюмер, новое событие. Пропускаем.
		r.log.Debug("unknown event type", slog.String("type", eventType))
		return nil
	}
}

// header возвращает значение первого заголовка с ключом key или "" — у kgo.Record
// заголовки лежат слайсом, прямого доступа по ключу нет.
func header(rec *kgo.Record, key string) string {
	for _, h := range rec.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func (r *OrderRouter) handleOrderCreated(ctx context.Context, rec *kgo.Record) error {
	var pb orderv1.OrderCreated
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(rec.Value, &pb); err != nil {
		return apperr.InvalidArgument().Wrap(err)
	}

	items := make([]domain.OrderItem, 0, len(pb.GetItems()))
	for _, it := range pb.GetItems() {
		items = append(items, domain.OrderItem{
			ID:          it.GetId(),
			ProductID:   it.GetProductId(),
			ProductName: it.GetProductName(),
			Quantity:    it.GetQuantity(),
			UnitPrice:   it.GetUnitPrice(),
		})
	}

	return r.notification.HandleOrderCreated(ctx, usecase.HandleOrderCreatedInput{
		// «Конверт» для inbox: event_id — ключ дедупликации, остальное — для диагностики.
		Message: domain.InboxEvent{
			EventID:     pb.GetEventId(),
			Topic:       rec.Topic,
			Partition:   rec.Partition,
			Offset:      rec.Offset,
			EventType:   domain.EventTypeOrderCreated,
			AggregateID: pb.GetOrderId(), // совпадает с string(rec.Key)
		},
		Payload: rec.Value, // сырое тело — в notifications.payload, не в inbox
		Event: domain.OrderCreated{
			EventID:         pb.GetEventId(),
			OrderID:         pb.GetOrderId(),
			UserID:          pb.GetUserId(),
			Items:           items,
			TotalAmount:     pb.GetTotalAmount(),
			DeliveryAddress: pb.GetDeliveryAddress(),
			CreatedAt:       pb.GetCreatedAt().AsTime(),
		},
	})
}
```

Пустой или не-UUID `event_id` (и `order_id`) даст ошибку на вставке в колонки `UUID`, то есть
такое сообщение — тоже контрактная ошибка; при желании проверяем явно до usecase.

`DiscardUnknown: true` — чтобы добавление поля в `OrderCreated` не ломало старых консьюмеров.

Неизвестный `event-type` игнорируем молча (это forward compatibility), а вот битый payload
известного типа — ошибка: значит, контракт нарушен, и это надо увидеть.

### 6.5 app.go

Файл `services/notification/internal/app/app.go`. К моменту, когда пишется этот раздел, в проекте уже есть `platform/closer` (закрытие ресурсов
через `Closer.Add` в порядке LIFO, см. `services/order/internal/app/app.go`) — новый `App`
notification-сервиса строится по той же схеме, а не через отдельные поля-хендлы и ручной вызов
`Close()` на каждом из них:

```go
type App struct {
	cfg      *config.Config
	log      *slog.Logger
	consumer *kafkaconsumer.Consumer
	router   *kafkarouter.OrderRouter // alias на internal/transport/kafka
	sender   *worker.SenderWorker
	closer   *closer.Closer
}

func New(ctx context.Context) (_ *App, err error) {
	cl := closer.New()
	defer func() {
		if err != nil {
			err = errors.Join(err, cl.Close(context.Background()))
		}
	}()

	// Config
	cfg, err := config.New()
	if err != nil {
		return nil, err
	}

	// Logger
	log := logger.New(
		logger.WithLevel(cfg.Log.Level),
		logger.WithFormat(cfg.Log.Format),
	).With(
		slog.String("app_name", cfg.App.Name),
	)
	slog.SetDefault(log)

	// Postgres
	pgPool, err := postgres.New(
		ctx,
		cfg.PG.DSN,
		postgres.WithMaxConns(cfg.PG.MaxConns),
	)
	if err != nil {
		return nil, err
	}
	cl.Add(closer.Wrap(pgPool.Close))

	// Kafka consumer
	consumer, err := kafkaconsumer.New(
		cfg.Kafka.Brokers,
		cfg.Kafka.ConsumerGroup,
		cfg.Kafka.Topics,
		kafkaconsumer.WithErrorHandler(func(ctx context.Context, err error) {
			log.ErrorContext(ctx, "kafka consumer", logger.Err(err))
		}),
		// Внутренние логи franz-go (ребалансы, переподключения) — в тот же slog.
		kafkaconsumer.WithClientOptions(kgo.WithLogger(kslog.New(log))),
	)
	if err != nil {
		return nil, err
	}
	cl.Add(closer.Wrap(consumer.Close))

	// Repository
	txManager := postgres.NewTxManager(pgPool)
	inboxRepo := repo.NewInboxRepo(pgPool)
	notificationRepo := repo.NewNotificationRepo(pgPool)

	// Usecase
	notificationUsecase := usecase.NewNotificationUsecase(usecase.NotificationUsecaseDeps{
		Tx:            txManager,
		Inbox:         inboxRepo,
		Notifications: notificationRepo,
		Log:           log,
	})
	senderUsecase := usecase.NewSenderUsecase(usecase.SenderUsecaseDeps{
		Store:  notificationRepo,
		Sender: notify.NewLogNotifier(log),
		Log:    log,
	})

	// Worker
	senderWorker := worker.NewSenderWorker(senderUsecase, 2*time.Second, log)

	// Router
	router := kafkarouter.NewOrderRouter(kafkarouter.OrderRouterDeps{
		NotificationUsecase: notificationUsecase,
		Log:                 log,
	})

	app := &App{
		cfg:      cfg,
		log:      log,
		consumer: consumer,
		router:   router,
		sender:   senderWorker,
		closer:   cl,
	}

	return app, nil
}

func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.Info("kafka consumer started",
			slog.Any("topics", a.cfg.Kafka.Topics),
			slog.String("group", a.cfg.Kafka.ConsumerGroup))
		defer a.log.Info("kafka consumer stopped")
		return a.consumer.Run(ctx, a.router)
	})

	g.Go(func() error {
		a.log.Info("sender worker started")
		defer a.log.Info("sender worker stopped")
		return a.sender.Run(ctx)
	})

	return g.Wait()
}

func (a *App) Close(ctx context.Context) error {
	return a.closer.Close(ctx)
}
```

`pgPool.Close` и `consumer.Close` регистрируются в `Closer` сразу после успешного создания —
так же, как `pgPool`/`userConn`/`productConn` в order-сервисе. Если `New` упадёт на любом шаге
после Postgres (например, при создании консьюмера), `defer` в начале функции закроет уже
открытые ресурсы через `cl.Close(...)`; `Close()` самого `App` — это всегда `a.closer.Close(ctx)`,
без ручного перечисления полей.

### 6.6 Sender-воркер

Приём события (раздел 6.4) в одной транзакции пишет `inbox_events` и `notifications` — и всё.
Отправляет отдельный воркер. Пока единственный канал — `log`, но схема сразу рассчитана на
реальные каналы, несколько экземпляров воркера и сбои:

```
Kafka → консьюмер → [inbox_events + notifications] (одна транзакция)
                              ↓
        N воркеров: захват пачки (SKIP LOCKED + аренда) → Send → sent | retry | failed
```

**Почему не «выбрал и отправил в одной транзакции».** Транзакция на время сетевых вызовов
держит соединение с БД и блокировки строк, а упавший воркер оставляет их до таймаута. Вместо
этого запись **захватывается** коротким запросом и получает срок аренды (`locked_until`);
результат отправки фиксируется отдельными короткими запросами.

**Репозиторий.** Четыре метода в `internal/repository/pg/notification.go` (пятый — `Create` из 6.4):

```go
// ClaimBatch захватывает до limit записей, готовых к отправке, и выдаёт им аренду на lease.
// SKIP LOCKED — реплики воркера берут разные записи и не ждут друг друга. Запись с живой
// арендой пропускается; если воркер упал, аренда истечёт, и запись подхватит другой.
func (r *NotificationRepo) ClaimBatch(
	ctx context.Context,
	limit int,
	lease time.Duration,
) ([]domain.Notification, error) {
	rows, err := postgres.DB(ctx, r.pool).Query(ctx,
		`UPDATE notifications
		 SET locked_until = now() + make_interval(secs => $2),
		     attempts     = attempts + 1
		 WHERE id IN (
		     SELECT id FROM notifications
		     WHERE status = 'pending'
		       AND next_attempt_at <= now()
		       AND (locked_until IS NULL OR locked_until < now())
		     ORDER BY next_attempt_at
		     LIMIT $1
		     FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id, event_id, event_type, channel, payload, attempts`,
		limit, lease.Seconds())
	if err != nil {
		return nil, pgerr.Map(err)
	}
	return pgx.CollectRows(rows, pgx.RowToStructByName[domain.Notification])
}

func (r *NotificationRepo) MarkSent(ctx context.Context, id string) error {
	_, err := postgres.DB(ctx, r.pool).Exec(ctx,
		`UPDATE notifications
		 SET status = 'sent', sent_at = now(), locked_until = NULL, last_error = NULL
		 WHERE id = $1`, id)
	return pgerr.Map(err)
}

// MarkRetry возвращает запись в очередь: не раньше next.
func (r *NotificationRepo) MarkRetry(ctx context.Context, id string, next time.Time, cause string) error {
	_, err := postgres.DB(ctx, r.pool).Exec(ctx,
		`UPDATE notifications
		 SET next_attempt_at = $2, last_error = $3, locked_until = NULL
		 WHERE id = $1`, id, next, cause)
	return pgerr.Map(err)
}

// MarkFailed выводит запись из очереди: дальше её разбирают вручную.
func (r *NotificationRepo) MarkFailed(ctx context.Context, id string, cause string) error {
	_, err := postgres.DB(ctx, r.pool).Exec(ctx,
		`UPDATE notifications
		 SET status = 'failed', last_error = $2, locked_until = NULL
		 WHERE id = $1`, id, cause)
	return pgerr.Map(err)
}
```

`attempts` увеличивается при захвате, поэтому после падения воркера посреди отправки счётчик
всё равно вырос, и «ядовитая» запись, на которой воркер падает, рано или поздно упрётся в
`maxAttempts`. В `domain.Notification` добавляется поле `Attempts int`.

**Usecase** (`internal/usecase/sender.go`):

```go
const (
	batchSize   = 50
	maxAttempts = 10
	sendTimeout = 5 * time.Second
	// leaseTTL должен быть больше худшего времени обработки пачки (batchSize * sendTimeout),
	// иначе аренда истечёт посреди пачки и записи уйдут второму воркеру — будут дубли.
	leaseTTL = 5 * time.Minute
)

// ErrPermanent — повтор не поможет (невалидный адрес и т. п.): сразу failed.
var ErrPermanent = errors.New("permanent send error")

type NotificationStore interface {
	ClaimBatch(ctx context.Context, limit int, lease time.Duration) ([]domain.Notification, error)
	MarkSent(ctx context.Context, id string) error
	MarkRetry(ctx context.Context, id string, next time.Time, cause string) error
	MarkFailed(ctx context.Context, id string, cause string) error
}

type Sender interface {
	Send(ctx context.Context, n domain.Notification) error
}

type SenderUsecaseDeps struct {
	Store  NotificationStore
	Sender Sender
	Log    *slog.Logger
}

type SenderUsecase struct {
	store  NotificationStore
	sender Sender
	log    *slog.Logger
}

func NewSenderUsecase(deps SenderUsecaseDeps) *SenderUsecase {
	return &SenderUsecase{store: deps.Store, sender: deps.Sender, log: deps.Log}
}

// RunOnce обрабатывает одну пачку. more == true — пачка была полной, значит, очередь, скорее
// всего, не пуста, и воркер не ждёт следующего тика. Ошибка отправки не прерывает пачку.
func (uc *SenderUsecase) RunOnce(ctx context.Context) (more bool, err error) {
	batch, err := uc.store.ClaimBatch(ctx, batchSize, leaseTTL)
	if err != nil {
		return false, err
	}
	for _, n := range batch {
		if err := uc.process(ctx, n); err != nil {
			return false, err // ошибка БД: остаток пачки вернётся по истечении аренды
		}
	}
	return len(batch) == batchSize, nil
}

func (uc *SenderUsecase) process(ctx context.Context, n domain.Notification) error {
	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	sendErr := uc.sender.Send(sendCtx, n)
	switch {
	case sendErr == nil:
		return uc.store.MarkSent(ctx, n.ID)
	case errors.Is(sendErr, ErrPermanent) || n.Attempts >= maxAttempts:
		uc.log.ErrorContext(ctx, "send failed permanently",
			slog.String("notification_id", n.ID), slog.Int("attempts", n.Attempts), slog.Any("error", sendErr))
		return uc.store.MarkFailed(ctx, n.ID, sendErr.Error())
	default:
		uc.log.WarnContext(ctx, "send failed, will retry",
			slog.String("notification_id", n.ID), slog.Int("attempts", n.Attempts), slog.Any("error", sendErr))
		return uc.store.MarkRetry(ctx, n.ID, time.Now().Add(backoff(n.Attempts)), sendErr.Error())
	}
}

// backoff — 5с, 10с, 20с… до 15 минут, с джиттером, чтобы повторы не шли синхронной волной.
func backoff(attempts int) time.Duration {
	d := min(5*time.Second<<min(attempts-1, 10), 15*time.Minute)
	return d/2 + rand.N(d/2)
}
```

`rand` — `math/rand/v2`.

**Семантика отправки.** «Хотя бы раз»: упали между `Send` и `MarkSent` — после истечения аренды
отправка повторится. Для реальных каналов (email/push) провайдеру передаётся ключ идемпотентности
(`notifications.id`), чтобы повтор не создал дубль.

**Цикл** (`internal/worker/sender.go`):

```go
type Runner interface {
	RunOnce(ctx context.Context) (more bool, err error)
}

type SenderWorker struct {
	uc       Runner
	interval time.Duration
	log      *slog.Logger
}

func NewSenderWorker(uc Runner, interval time.Duration, log *slog.Logger) *SenderWorker {
	return &SenderWorker{uc: uc, interval: interval, log: log}
}

// Run блокируется до отмены ctx.
func (w *SenderWorker) Run(ctx context.Context) error {
	t := time.NewTicker(w.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			// Пока пачки полные — не ждём тика: иначе пропускная способность ограничена
			// batchSize/interval (50 записей за 2 с — всего 25/с).
			for more := true; more && ctx.Err() == nil; {
				var err error
				more, err = w.uc.RunOnce(ctx)
				if err != nil {
					w.log.ErrorContext(ctx, "sender tick failed", slog.Any("error", err))
					break
				}
			}
		}
	}
}
```

Ошибка тика только логируется: воркер не должен падать из-за временной проблемы с БД. В
`app.go` воркер запускается в `errgroup` рядом с консьюмером (раздел 6.5) и завершается по
`ctx.Done()` — закрывать нечего.

**Канал `log`.** `LogNotifier` (`internal/notify/log.go`) — реализация `usecase.Sender`:

```go
package notify

type LogNotifier struct {
	log *slog.Logger
}

func NewLogNotifier(log *slog.Logger) *LogNotifier {
	return &LogNotifier{log: log}
}

func (n *LogNotifier) Send(ctx context.Context, nt domain.Notification) error {
	n.log.InfoContext(ctx, "notification sent",
		slog.String("channel", nt.Channel),
		slog.String("event_id", nt.EventID),
	)
	return nil
}
```

Отдельным интерфейсом `Sender`, а не прямым `slog` в usecase, — чтобы потом подменить на
письмо/пуш, не трогая usecase. Когда каналов станет несколько, `Send` выбирается по
`n.Channel` (маршрутизатор отправителей).

**Когда появится email: `user_id` и `recipient`.** Отдельной миграцией добавляются колонки
`user_id` (при приёме берётся из `OrderCreated.UserID`; для уже накопленных строк — бэкфилл
из `payload->>'user_id'`: колонка добавляется как `NULL`, заполняется, затем `SET NOT NULL`) и `recipient`; заполняет `recipient`
**воркер**: по `user_id` идёт в user-service по gRPC за контактами прямо перед `Send`. Не в транзакции приёма: сетевой вызов держит соединение с БД, и падение user-service
остановит консьюмер. Если user-service недоступен, запись остаётся `pending` и уйдёт на
следующем тике. Контакты принадлежат user-service; email в payload события не кладём
(устареет при смене адреса).

**Масштабирование и нагрузка.**

* **Реплики воркера.** Сколько угодно: `SKIP LOCKED` разводит их по разным записям, аренда
  страхует от падения. Отдельная координация не нужна.
* **Пропускная способность** = реплики × `batchSize` пачек подряд без ожидания тика. Если
  `Send` медленный, пачка обрабатывается последовательно — тогда растят число реплик или
  параллелят отправку внутри пачки (ограниченный пул горутин; `leaseTTL` пересчитывается).
* **Лимиты каналов.** У провайдеров бывают rate limit — ограничитель ставится на реализацию
  `Sender` конкретного канала, а не в usecase.
* **Приём.** Масштабируется партициями топика и числом консьюмеров в группе; при большом потоке
  консьюмер может писать пачку сообщений одной транзакцией.
* **Рост таблицы.** Частичный индекс держит выборку воркера быстрой при любом размере таблицы,
  но сама таблица растёт: `sent` чистится батчами по `sent_at`, `failed` хранится дольше —
  их разбирают вручную:

  ```sql
  DELETE FROM notifications
  WHERE id IN (
      SELECT id FROM notifications
      WHERE status = 'sent' AND sent_at < now() - interval '30 days'
      LIMIT 1000
  );
  ```

  Партиционирование по `created_at` (отбрасывание старых секций вместо `DELETE`) — только при
  реальной необходимости: `UNIQUE (event_id, channel)` придётся расширить ключом секции, и вторая
  линия дедупликации ослабнет.
* **Задержка** ограничена интервалом тика. Если понадобится мгновеннее (WebSocket), приём шлёт
  `NOTIFY` после коммита, а воркер просыпается по `LISTEN`; тик остаётся страховкой на случай
  потерянного сигнала.

**Наблюдаемость.** Без метрик отказоустойчивость не видна. Минимум: возраст самой старой
`pending` записи (`now() - min(next_attempt_at)`) — главный сигнал, что воркеры не справляются;
число `failed`; число повторов и длительность `Send` по каналам. Алерт — на растущий возраст
`pending` и на ненулевой прирост `failed`.

**Когда polling перестанет хватать.** Если поток устойчиво дойдёт до тысяч уведомлений в секунду,
таблица как очередь станет узким местом: задачи отправки выносят в отдельный топик, а
`notifications` остаётся историей.

---

## 7. Семантика доставки — сводка

| Участок | Гарантия | Чем обеспечена |
|---|---|---|
| usecase → БД | atomic | одна транзакция на `orders` + `order_items` + `outbox_events` |
| БД → Debezium | at-least-once | позиция в WAL коммитится периодически; после падения Connect перечитает хвост |
| Debezium → Redpanda | at-least-once | ретраи продюсера; при рестарте возможен повтор последних сообщений |
| Redpanda → consumer | at-least-once | ручной коммит оффсета после обработки |
| consumer → `notifications` | **effectively-once** | inbox: `INSERT ON CONFLICT DO NOTHING` + запись в `notifications` в одной транзакции |
| `notifications` → канал | at-least-once | воркер: захват с арендой, `Send`, затем `MarkSent`; упал между ними — аренда истекает, запись уходит повторно (нужен ключ идемпотентности у провайдера); сбои → ретраи с паузой, затем `failed` |

Дубликаты на каждом шаге — норма, а не авария. Именно поэтому inbox обязателен, а не «на всякий
случай». Exactly-once в транспорте не достигается вообще — достигается идемпотентность эффекта.

**Порядок** гарантирован только внутри партиции. Все события одного заказа имеют один ключ
(`order_id`) → одна партиция → порядок сохранён. Между разными заказами порядка нет и не нужно.

**Про канал `log` честно:** сейчас эффект — печать в stdout, и дубликат не страшен (в худшем
случае строка напечатается дважды). Inbox и `notifications` тут — отработка паттерна и задел:
как только появится реальный канал (email/push), дедупликация станет обязательной, а повторы
воркера придётся гасить ключом идемпотентности.

---

## 8. Конфигурация проекта

### .env order-service

Ничего нового не требуется — order-сервис про Kafka не знает вообще. Это важное свойство схемы
с Debezium: единственная зависимость сервиса — его собственная БД.

### Корневой Taskfile.yml

`services/notification` сейчас отсутствует в `MODULES` — добавить. Плюс новые задачи:

```yaml
vars:
  MODULES: platform,proto,services/gateway,services/order,services/user,services/product,services/notification
  DB_SERVICES: order,user,product,notification

tasks:
  connect-register:
    desc: Register the Debezium outbox connector
    cmds:
      - |
        curl -sS -X PUT -H 'Content-Type: application/json' \
          --data @deploy/debezium/order-outbox.json \
          http://localhost:8083/connectors/order-outbox/config

  connect-status:
    desc: Show connector status
    cmds:
      - curl -sS http://localhost:8083/connectors/order-outbox/status

  connect-delete:
    desc: Delete the connector (replication slot stays!)
    cmds:
      - curl -sS -X DELETE http://localhost:8083/connectors/order-outbox

  topic-consume:
    desc: Tail the order events topic
    cmds:
      - docker exec -it redpanda rpk topic consume order.events.v1 --offset start
```

В `watch-all` добавить `notification`.

---

## 9. Порядок внедрения

Каждый шаг проверяем до перехода к следующему — иначе при первой же ошибке непонятно, где искать.

1. **Миграция outbox** в order-сервисе. `task -d services/order migrate-up`.
   Чистку outbox по времени можно добавить позже, когда всё заработает.
2. **`platform/postgres/tx.go`**: `Executor`, `Exec`, `TxManager`. Перевести `OrderRepo.Create`
   на `postgres.DB(ctx, r.pool)`, транзакцию убрать.
3. **`events.proto`** + `task -d proto generate`.
4. **`internal/event`, `OutboxRepo`, usecase** с `WithinTx`. Проверка: вызвать `CreateOrder`
   через grpcui → в `outbox_events` появилась строка, `payload` — валидный JSON.
   На этом шаге брокера ещё нет вообще, и это нормально.
5. **compose: `wal_level=logical`** для order-postgres → `docker compose up -d` → проверить
   `SHOW wal_level`.
6. **compose: redpanda + console + debezium** → `rpk cluster health`, Console на `:3000`,
   `curl localhost:8083/connector-plugins` (должен быть `PostgresConnector`).
7. **Регистрация коннектора** → `task connect-status` (`RUNNING`, без `FAILED` в tasks).
   Создать заказ → сообщение в `order.events.v1`. Смотреть в Console или `rpk topic consume`.
   **До написания консьюмера убедиться, что сообщение в топике правильной формы** — ключ,
   заголовки, payload.
8. **notification-postgres + миграции inbox и notifications**, поправить `.env`.
9. **`platform/kafka/kafkaconsumer`** — сначала просто печать в лог того, что приехало, без БД.
   `franz-go` добавляется в `platform/go.mod` (`go get github.com/twmb/franz-go` в модуле
   `platform`); убедиться, что notification подхватывает локальный `platform` так же, как
   остальные сервисы (replace/workspace), и сделать `go mod tidy` в обоих модулях.
10. **domain/repository/usecase/transport** в notification: inbox, `notifications`, Unit of Work.
    Проверка: создать заказ → в `notifications` строка `pending` (до запуска воркера).
    **Sender-воркер** (раздел 6.6): через пару секунд строка `sent` и запись `notification sent` в логе.
11. **Проверка дедупликации**: остановить сервис, `rpk group seek notification --to start`,
    запустить → события перечитаются, но в stdout ничего нового и новых строк в `notifications`
    нет (все `event_id` уже в inbox).

---

## 10. Диагностика

```bash
# Слот репликации: активен ли, какой лаг по WAL
docker exec -it order-postgres psql -U postgres -d orderdb -c "
  SELECT slot_name, active, restart_lsn,
         pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS lag
  FROM pg_replication_slots;"

# Публикация: какие таблицы реально захвачены
docker exec -it order-postgres psql -U postgres -d orderdb -c "
  SELECT * FROM pg_publication_tables;"

# Публикация: по умолчанию dbz_publication FOR ALL TABLES (puballtables = t), все операции
docker exec -it order-postgres psql -U postgres -d orderdb -c "
  SELECT pubname, pubinsert, pubupdate, pubdelete, pubtruncate FROM pg_publication;"

# Статус коннектора и задач
curl -sS http://localhost:8083/connectors/order-outbox/status | jq

# Логи коннектора (там же трассы падений SMT)
docker logs -f debezium

# Топики и сообщения
docker exec -it redpanda rpk topic list
docker exec -it redpanda rpk topic consume order.events.v1 --offset start --format '%k | %h{%k=%v;} | %v\n'

# Лаг консьюмер-группы
docker exec -it redpanda rpk group describe notification
```

---

## 11. Грабли

1. **`wal_level` не применился.** Меняется только рестартом контейнера; `docker compose restart`
   после правки `command` обязателен. Если контейнер создавался раньше — `docker compose up -d`
   пересоздаст его с новой командой.

2. **Слот репликации не удаляется вместе с коннектором.** `DELETE /connectors/...` удаляет
   коннектор, но слот `debezium` остаётся в PostgreSQL и **продолжает держать WAL**.
   Диск растёт при полностью «выключенном» CDC. Удалять руками:
   `SELECT pg_drop_replication_slot('debezium');`. В проде — алерт на лаг слота.

3. **Один слот = один коннектор.** Два коннектора с одинаковым `slot.name` (по умолчанию —
   `debezium`) подерутся за слот (`replication slot is active for PID ...`). Для второго
   коннектора к той же БД — свои `slot.name` и `publication.name`. Коннектор к другой БД — это
   отдельный Postgres, там конфликта нет.

4. **`decoderbufs` по умолчанию.** Забыть `plugin.name=pgoutput` → коннектор падает на старте
   с ошибкой о недоступном плагине декодирования.

5. **Первый запуск переотправил все старые события.** В минимальном конфиге `snapshot.mode`
   дефолтный (`initial`): Debezium читает всю outbox-таблицу, и `EventRouter` отправляет её
   строки. Дубликаты отсечёт inbox; если переотправка не нужна — `snapshot.mode=no_data`
   (раздел 5.4).

6. **Консьюмер с хоста не видит брокер.** Симптом: `dial tcp: lookup redpanda: no such host`.
   Причина — advertised-адрес internal listener'а. Сервисы на хосте ходят на `localhost:19092`.

7. **Заголовки приезжают не в том виде, в каком ожидаешь.** Конвертер заголовков в Connect
   сериализует значения по-своему; строка может приехать с кавычками или без в зависимости
   от версии/настроек (в стенде `rpk` показал значения без кавычек, но на это опираться нельзя).
   Поэтому `event_id` продублирован внутри payload — на заголовки опираемся для маршрутизации,
   а для дедупликации берём значение из payload. Кроме `id` и `event-type` в заголовках есть
   служебные `__debezium.context.*` — их игнорируем.

8. **UPDATE по outbox-таблице.** SMT их только логирует (`table.op.invalid.behavior=warn`) —
   изменение «потеряется» молча. Outbox — append-only, обновлять строки в ней нельзя.

9. **`value` в топике — экранированная строка.** Забыт `table.expand.json.payload=true`
   или `payload` объявлен `TEXT` вместо `jsonb`.

10. **Оффсет закоммичен, а обработка упала.** Проверить, что стоит `DisableAutoCommit()` —
    с автокоммитом franz-go двигает оффсеты по таймеру независимо от обработки.

11. **WAL растёт, хотя в outbox давно не пишут.** Слот держит WAL до последнего подтверждённого
    LSN, а Debezium подтверждает его только когда видит событие из своих таблиц. Если в БД
    активно пишут в другие таблицы (`orders`), а в `outbox_events` тишина — WAL пухнет.
    В минимальном конфиге heartbeat'а нет, поэтому на активной БД смотреть лаг слота
    (раздел 10) и подключать heartbeat (раздел 5.4).

12. **Упавшее сообщение не приезжает повторно до рестарта.** Консьюмер не коммитит оффсет,
    но и не откатывает позицию чтения (`SetOffsets`). Следующий poll продолжает с записи
    после упавшей, а если потом закоммитится более поздняя запись той же партиции — упавшая
    потеряна навсегда. См. раздел 6.3, решение 3.

13. **`DELETE` из outbox падает с `does not have a replica identity and publishes deletes`.**
    Возможно только у таблицы без PK. У `outbox_events` PK есть, и для неё `DELETE` проходит
    (проверено). Если ошибка всё же появилась — проверить, что первичный ключ на месте, а не
    выставлять replica identity.

---

## 12. Осознанно отложено

* **Всё, что убрано из минимального конфига** — `snapshot.mode`, heartbeat, `filtered`-публикация,
  insert-only, число партиций и `topic.creation.*`, replication factor, Connect в Console.
  Таблица с причинами и условиями возврата — в разделе 5.4.
* **protobuf-binary на проводе** (`bytea` + `BinaryDataConverter` + delegate-конвертер).
  Правильнее для прода с точки зрения контроля схемы и размера; дороже в отладке.
* **Schema Registry** (Redpanda поставляется со своим на `:18081`). Нужен, когда потребителей
  станет больше одного и схемы начнут эволюционировать независимо.
* **`platform/kafka/kafkaproducer`** — сознательно не заводим. В этой схеме продюсер не нужен
  нигде: публикация — работа Debezium, а не сервисов, и сервис, который сам пишет в брокер,
  снова получает dual-write, ради устранения которого всё и затевалось. Симметричный пакет
  появится только тогда, когда возникнет событие, не привязанное к записи в БД (например,
  чисто технические/уведомительные сообщения) — и это будет осознанное исключение.
* **DLQ-топик** для «ядовитых» сообщений и счётчик попыток. Без него консьюмер зациклится
  на неразбираемом сообщении: битый payload известного типа блокирует свою партицию, пока
  сообщение не будет исправлено или пропущено вручную.
* **Доработки sender-воркера** (базовая схема — раздел 6.6): `user_id` и `recipient` (воркер
  резолвит адрес через user-service, не в транзакции приёма), реальные каналы (email/push) с
  идемпотентным ключом у провайдера, параллельная отправка внутри пачки, метрики и алерты,
  `LISTEN/NOTIFY` для мгновенного пробуждения, партиционирование `notifications`, вынос задач
  отправки в отдельный топик при очень большом потоке.
* **Чистка outbox/inbox** по расписанию (или partitioning по `created_at`).
* **Трассировка через границу брокера**: Debezium умеет протаскивать span context
  (`tracing.span.context.field` в SMT) — вернуться к этому вместе с observability из TODO.
* **Идемпотентность на входе `CreateOrder`** (ключ идемпотентности в gRPC) — отдельная задача,
  уже есть в TODO. Outbox/inbox её не заменяют: они защищают доставку события, а не повторный
  вызов API.
