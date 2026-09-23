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
                                  │  order.events.v1  │  3 партиции
                                  └─────────┬─────────┘
                                            │ consumer group notification
                                            ▼
                ┌───────────────── notification-service (Go) ──────────────┐
                │ transport/kafka  → usecase.HandleOrderCreated            │
                │   txManager.WithinTx(ctx, func(ctx) error {              │
                │       ok := inbox.Claim(ctx, msg)  // ON CONFLICT DO NOTHING
                │       if !ok { return nil }        // дубль — выходим     │
                │       return notifier.Notify(ctx, event) // пока log.Info │
                │   })                                                     │
                │ commit offset только после успешной транзакции           │
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
import "order/v1/order.proto";

// Событие: заказ создан. Публикуется через outbox.
message OrderCreated {
  // Идентификатор события = outbox_events.id. Дублируется в payload намеренно:
  // потребитель не зависит от того, как брокер/коннектор передал заголовки.
  string event_id = 1;

  string order_id = 2;
  string user_id = 3;
  repeated OrderItem items = 4;
  int64 total_amount = 5;
  string delivery_address = 6;
  google.protobuf.Timestamp created_at = 7;
}
```

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

Важная деталь: `protojson` сериализует `int64` как **строку** (`"total_amount": "1500"`).
Это нормально, `protojson.Unmarshal` принимает и строку, и число. Но если кто-то будет читать
топик не через protojson — он должен быть к этому готов.

### 3.3 Топики и версионирование

| Что | Значение |
|---|---|
| Имя топика | `order.events.v1` |
| Формируется как | `route.topic.replacement = ${routedByValue}.events.v1`, где `routedByValue` = `outbox_events.aggregate_type` = `order` |
| Ключ сообщения | `aggregate_id` = `order_id` (UUID-строка) |
| Партиций | 3 |
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

-- Для ручной чистки/диагностики по времени.
CREATE INDEX idx_outbox_events_created_at
    ON outbox_events (created_at);

-- Debezium читает WAL; DELETE нужен old-tuple с ключом.
-- DEFAULT (= PK) достаточно, оставляем явно для наглядности.
ALTER TABLE outbox_events REPLICA IDENTITY DEFAULT;
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
| `created_at` | время события; становится timestamp'ом сообщения |

Имена колонок подобраны так, чтобы быть близко к дефолтам SMT, но в snake_case, как в остальном
проекте. Дефолты Debezium — `aggregatetype`/`aggregateid`, поэтому в конфиге коннектора
маппинг задан явно (раздел 5.4).

Почему `payload` — `jsonb`, а не `json`: `jsonb` нормализует документ и валидирует его на входе,
битый JSON не доедет до брокера. Цена — потеря порядка ключей, что нам безразлично.

Чего в таблице **нет**: колонок `published_at`, `attempts`, `status`. Их не должно быть —
это атрибуты поллера, которого у нас нет. Debezium отслеживает позицию в WAL, а не состояние строк.

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

// Exec возвращает транзакцию из контекста, если она там есть, иначе пул.
// Репозитории всегда ходят в БД через него и не знают, внутри транзакции они или нет.
func Exec(ctx context.Context, pool *pgxpool.Pool) Executor {
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
		_ = tx.Rollback(ctx) // no-op после успешного Commit
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
	db := postgres.Exec(ctx, r.pool)

	if err := r.insertOrder(ctx, db, order); err != nil {
		return err
	}
	return r.insertItems(ctx, db, order.Items)
}
```

(`insertOrder`/`insertItems` меняют сигнатуру с `pgx.Tx` на `postgres.Executor` — тело остаётся
прежним, включая `CopyFrom`.)

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
	if _, err := postgres.Exec(ctx, r.pool).Exec(
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

	items := make([]*orderv1.OrderItem, 0, len(order.Items))
	for _, item := range order.Items {
		items = append(items, &orderv1.OrderItem{
			Id:          item.ID,
			ProductId:   item.ProductID,
			ProductName: item.ProductName,
			Quantity:    item.Quantity,
			UnitPrice:   item.UnitPrice,
		})
	}

	payload, err := protojson.Marshal(&orderv1.OrderCreated{
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
		CreatedAt:     time.Now().UTC(),
	}, nil
}
```

Почему отдельный пакет, а не метод домена: сериализация в protojson тянет зависимость от
сгенерированного `orderv1`, а домен должен остаться чистым. Usecase видит это через интерфейс
(объявленный у потребителя, как `OrderRepo`/`UserProvider` сейчас).

### 4.5 Usecase

```go
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
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

### 4.6 Нужно ли чистить outbox

Таблица растёт монотонно. Два подхода:

1. **Хранить N дней** (рекомендуется здесь). Строки остаются, их видно при отладке, всегда можно
   сравнить «что лежит в БД» с «что пришло в топик». Чистка — `DELETE FROM outbox_events WHERE
   created_at < now() - interval '7 days'` раз в сутки (воркер/`pg_cron`). Debezium DELETE-события
   отбрасывает сам (SMT фильтрует их, плюс мы ставим `skipped.operations=u,d,t`).
2. **`INSERT` + `DELETE` в одной транзакции.** Таблица всегда пустая, но событие в WAL есть и
   Debezium его увидит. Экономит место, но лишает отладочного следа и сильно запутывает при
   первом знакомстве.

Второй вариант — законная оптимизация для прода, но включать его стоит после того, как первый
заработает и будет понятен.

---

## 5. Инфраструктура

### 5.1 PostgreSQL: logical replication

Для CDC нужен `wal_level=logical`. Правим `order-postgres` в `docker-compose.yml`:

```yaml
  order-postgres:
    container_name: order-postgres
    image: postgres:18
    command:
      - postgres
      - -c
      - wal_level=logical
      - -c
      - max_wal_senders=10
      - -c
      - max_replication_slots=10
    environment:
      - POSTGRES_USER=postgres
      - POSTGRES_PASSWORD=postgres
      - POSTGRES_DB=orderdb
    ports:
      - "5432:5432"
    volumes:
      - order-postgres-data:/var/lib/postgresql/18/docker
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres -d orderdb"]
      interval: 5s
      timeout: 3s
      retries: 10
```

Проверка: `docker exec -it order-postgres psql -U postgres -d orderdb -c 'SHOW wal_level;'`
должно вернуть `logical`. Менять `wal_level` можно только рестартом.

Пользователь: локально ходим под `postgres` (суперюзер, атрибут `REPLICATION` есть по умолчанию).
В проде так нельзя — нужен отдельный пользователь с `REPLICATION`, `CREATE` на БД и `SELECT`
на захватываемых таблицах.

### 5.2 Redpanda + Console

```yaml
  redpanda:
    container_name: redpanda
    image: redpandadata/redpanda:v26.2.3
    command:
      - redpanda start
      - --mode dev-container
      - --smp 1
      - --memory 1G
      - --node-id 0
      - --kafka-addr internal://0.0.0.0:9092,external://0.0.0.0:19092
      - --advertise-kafka-addr internal://redpanda:9092,external://localhost:19092
      - --schema-registry-addr internal://0.0.0.0:8081,external://0.0.0.0:18081
      - --rpc-addr redpanda:33145
      - --advertise-rpc-addr redpanda:33145
    ports:
      - "19092:19092"   # для сервисов, запущенных на хосте через Air
      - "18081:18081"   # Schema Registry — задел под protobuf/avro, см. раздел 12
      - "9644:9644"     # admin API
    healthcheck:
      test: ["CMD-SHELL", "rpk cluster health | grep -E 'Healthy:.+true'"]
      interval: 10s
      timeout: 5s
      retries: 10

  redpanda-console:
    container_name: redpanda-console
    image: redpandadata/console:v3.12.0
    environment:
      KAFKA_BROKERS: redpanda:9092
      CONNECT_ENABLED: "true"
      CONNECT_CLUSTERS_0_NAME: debezium
      CONNECT_CLUSTERS_0_URL: http://connect:8083
    ports:
      - "8080:8080"
    depends_on:
      redpanda:
        condition: service_healthy
```

**Два listener'а — не прихоть.** Сервисы запускаются на хосте (`task dev` → Air), а Connect —
в docker. Поэтому:

* Connect → `redpanda:9092` (internal);
* `notification-service` с хоста → `localhost:19092` (external).

Если объявить только один listener с `advertise-kafka-addr redpanda:9092`, клиент с хоста
подключится к брокеру, получит в metadata адрес `redpanda:9092` и отвалится по DNS.

Порт `8080` занят Console — проверь, что он не конфликтует с gateway (у gateway HTTP-порт свой,
см. `services/gateway/.env`; если совпадает — сдвинь Console на `8081`).

### 5.3 Kafka Connect с Debezium

```yaml
  connect:
    container_name: connect
    image: quay.io/debezium/connect:3.6.3.Final
    environment:
      BOOTSTRAP_SERVERS: redpanda:9092
      GROUP_ID: delivery-connect
      CONFIG_STORAGE_TOPIC: _connect_configs
      OFFSET_STORAGE_TOPIC: _connect_offsets
      STATUS_STORAGE_TOPIC: _connect_statuses
      CONFIG_STORAGE_REPLICATION_FACTOR: 1
      OFFSET_STORAGE_REPLICATION_FACTOR: 1
      STATUS_STORAGE_REPLICATION_FACTOR: 1
      KEY_CONVERTER: org.apache.kafka.connect.storage.StringConverter
      VALUE_CONVERTER: org.apache.kafka.connect.json.JsonConverter
      CONNECT_KEY_CONVERTER_SCHEMAS_ENABLE: "false"
      CONNECT_VALUE_CONVERTER_SCHEMAS_ENABLE: "false"
    ports:
      - "8083:8083"
    depends_on:
      redpanda:
        condition: service_healthy
      order-postgres:
        condition: service_healthy
```

`replication.factor=1` обязателен: по умолчанию Connect создаёт свои служебные топики с RF=3,
а у нас один брокер — коннект не стартует.

### 5.4 Конфигурация коннектора

Кладём в `deploy/debezium/order-outbox.json` (только объект `config`, чтобы регистрировать
идемпотентным `PUT /connectors/{name}/config`):

```json
{
  "connector.class": "io.debezium.connector.postgresql.PostgresConnector",
  "tasks.max": "1",

  "database.hostname": "order-postgres",
  "database.port": "5432",
  "database.user": "postgres",
  "database.password": "postgres",
  "database.dbname": "orderdb",

  "topic.prefix": "orderdb",
  "plugin.name": "pgoutput",
  "slot.name": "order_outbox_slot",
  "publication.name": "order_outbox_pub",
  "publication.autocreate.mode": "filtered",

  "table.include.list": "public.outbox_events",
  "snapshot.mode": "no_data",
  "skipped.operations": "u,d,t",
  "tombstones.on.delete": "false",

  "heartbeat.interval.ms": "10000",
  "heartbeat.action.query": "INSERT INTO debezium_heartbeat (id, ts) VALUES (1, now()) ON CONFLICT (id) DO UPDATE SET ts = now()",

  "predicates": "isOutbox",
  "predicates.isOutbox.type": "org.apache.kafka.connect.transforms.predicates.TopicNameMatches",
  "predicates.isOutbox.pattern": "orderdb\\.public\\.outbox_events",

  "transforms": "outbox",
  "transforms.outbox.type": "io.debezium.transforms.outbox.EventRouter",
  "transforms.outbox.predicate": "isOutbox",
  "transforms.outbox.table.field.event.id": "id",
  "transforms.outbox.table.field.event.key": "aggregate_id",
  "transforms.outbox.table.field.event.payload": "payload",
  "transforms.outbox.table.field.event.timestamp": "created_at",
  "transforms.outbox.table.expand.json.payload": "true",
  "transforms.outbox.table.fields.additional.placement": "type:header:event-type,aggregate_type:header:aggregate-type",
  "transforms.outbox.route.by.field": "aggregate_type",
  "transforms.outbox.route.topic.replacement": "${routedByValue}.events.v1",

  "key.converter": "org.apache.kafka.connect.storage.StringConverter",
  "value.converter": "org.apache.kafka.connect.json.JsonConverter",
  "value.converter.schemas.enable": "false",

  "topic.creation.enable": "true",
  "topic.creation.default.partitions": "3",
  "topic.creation.default.replication.factor": "1",
  "topic.creation.default.cleanup.policy": "delete",
  "topic.creation.default.retention.ms": "604800000",

  "errors.log.enable": "true",
  "errors.log.include.messages": "true"
}
```

Построчно о неочевидном:

* **`plugin.name=pgoutput`** — задать явно. Дефолт коннектора до сих пор `decoderbufs`, а это
  внешнее расширение, которого в образе `postgres:18` нет. `pgoutput` встроен в PostgreSQL.
* **`topic.prefix`** — обязателен, но в топики событий не попадает: имя формирует SMT. Префикс
  остаётся в именах «сырых» CDC-топиков (`orderdb.public.outbox_events`) и в служебных данных,
  поэтому он же фигурирует в `predicates.isOutbox.pattern`.
* **`snapshot.mode=no_data`** — стартуем без снапшота. Иначе при первом запуске Debezium прочитает
  всю outbox-таблицу и переотправит все старые события. `never` из Debezium 2.x переименован
  в `no_data`.
* **`publication.autocreate.mode=filtered`** — публикация создаётся только для таблиц из
  `table.include.list`. Дефолт `all_tables` отдал бы в WAL-декодер всю БД.
* **`predicates` + `TopicNameMatches`** — SMT должен применяться только к сообщениям из
  outbox-таблицы. Heartbeat и служебные сообщения имеют другую структуру, и `EventRouter` на них
  падает. Это рекомендация самой документации Debezium.
* **`table.fields.additional.placement`** — единственный способ протащить колонку `type` наружу.
  Опции вида `table.field.event.type` не существует: SMT по умолчанию кладёт в заголовки только
  `id`. Поэтому тип события уезжает заголовком `event-type` — и продублирован внутри payload
  (поле `event_id`/`OrderCreated` по смыслу), чтобы потребитель мог не зависеть от заголовков.
* **`table.expand.json.payload=true`** — без него значение сообщения будет JSON-**строкой**
  с экранированными кавычками, и потребителю пришлось бы сначала разэкранировать её, а потом
  парсить. С ним в топике лежит нормальный объект.
* **`skipped.operations=u,d,t`** — в outbox допустим только INSERT. UPDATE — ошибка (SMT их
  и так ругает по `table.op.invalid.behavior=warn`), DELETE — это чистка, наружу её не нужно.
* **`heartbeat.*`** — важнее, чем кажется. Слот репликации держит WAL до последнего
  подтверждённого LSN. Если в `orderdb` идёт активность (записи в `orders`), а в `outbox_events`
  какое-то время нет — Debezium не подтверждает LSN, и WAL пухнет на диске. Heartbeat заставляет
  его регулярно двигать позицию. Под `heartbeat.action.query` нужна таблица (миграция ниже).

Таблица для heartbeat — `services/order/migrations/pg/000003_create_debezium_heartbeat.up.sql`:

```sql
CREATE TABLE debezium_heartbeat (
    id BIGINT PRIMARY KEY,
    ts TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO debezium_heartbeat (id, ts) VALUES (1, NOW());
```

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

### 6.2 Миграция inbox

`services/notification/migrations/pg/000001_create_inbox_messages_table.up.sql`:

```sql
CREATE TABLE inbox_messages (
    event_id     UUID PRIMARY KEY,
    topic        TEXT        NOT NULL,
    partition    INT         NOT NULL,
    msg_offset   BIGINT      NOT NULL,
    event_type   TEXT        NOT NULL,
    aggregate_id UUID        NOT NULL,
    payload      JSONB       NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_inbox_messages_processed_at
    ON inbox_messages (processed_at);
```

`event_id` — первичный ключ, и это весь механизм дедупликации. `topic/partition/msg_offset`
хранятся для диагностики («откуда приехало»), уникальности по ним не требуем: после ребаланса
одно и то же событие может прийти с тем же оффсетом повторно — его отсечёт PK.

`offset` — зарезервированное слово в SQL, поэтому колонка называется `msg_offset`.

### 6.3 platform/kafka/kafkaconsumer

По конвенции проекта (`platform/grpc/grpcserver`, `platform/http/httpserver`) — тонкая обёртка
жизненного цикла с функциональными опциями. Пакет называем по протоколу (`kafka`), а не по
вендору (`redpanda`): протокол Kafka-совместимый, брокер заменяем. Что бы ни стояло за адресом
брокера — Redpanda, сама Kafka, WarpStream — код консьюмера остаётся тем же.

Клиент — `github.com/twmb/franz-go`: чистый Go без cgo (в отличие от `confluent-kafka-go`,
который тянет `librdkafka` и ломает кросс-компиляцию), consumer group'ы из коробки,
в апстриме тестируется в том числе против Redpanda.

```go
package kafkaconsumer

type Message struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Value     []byte
	Headers   map[string]string
	Timestamp time.Time
}

type Handler interface {
	Handle(ctx context.Context, msg Message) error
}

type Consumer struct {
	client         *kgo.Client
	log            *slog.Logger
	maxPollRecords int
}

func New(brokers []string, group string, topics []string, opts ...Option) (*Consumer, error) {
	c := &Consumer{
		log:            slog.Default(),
		maxPollRecords: defaultMaxPollRecords,
	}
	for _, opt := range opts {
		opt(c)
	}

	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		// Оффсеты коммитим руками — только после успешной обработки.
		kgo.DisableAutoCommit(),
		// Ребаланс не должен случиться, пока батч в обработке.
		kgo.BlockRebalanceOnPoll(),
	)
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
		if ctx.Err() != nil {
			return nil
		}
		if err := fetches.Err0(); err != nil && errors.Is(err, kgo.ErrClientClosed) {
			return nil
		}
		fetches.EachError(func(t string, p int32, err error) {
			c.log.Error("kafka fetch error",
				slog.String("topic", t), slog.Int("partition", int(p)), logger.Err(err))
		})

		var processed []*kgo.Record

		// Партиции независимы, но внутри партиции строго по порядку.
		fetches.EachPartition(func(p kgo.FetchTopicPartition) {
			for _, rec := range p.Records {
				if err := h.Handle(ctx, toMessage(rec)); err != nil {
					// Дальше по этой партиции не идём: порядок важнее пропускной способности.
					c.log.Error("handle message", logger.Err(err), ...)
					return
				}
				processed = append(processed, rec)
			}
		})

		if len(processed) > 0 {
			if err := c.client.CommitRecords(ctx, processed...); err != nil {
				c.log.Error("commit offsets", logger.Err(err))
			}
		}

		c.client.AllowRebalance()
	}
}

func (c *Consumer) Close() { c.client.Close() }
```

Три принципиальных решения в этом коде:

1. **`DisableAutoCommit` + ручной `CommitRecords` после обработки.** Автокоммит сдвигает оффсет
   по таймеру, независимо от того, обработано сообщение или нет, — это превращает at-least-once
   в at-most-once (потеря при падении).
2. **`BlockRebalanceOnPoll` + `AllowRebalance`.** Без этого партиция может уехать к другому
   консьюмеру прямо во время обработки батча, и `CommitRecords` для чужой партиции не пройдёт.
3. **Остановка на первой ошибке в партиции.** Пропустить сообщение и пойти дальше — значит
   нарушить порядок и потерять событие. Не коммитим → на следующем poll оно приедет снова.
   Это и есть ретрай.

Зацикливание на «ядовитом» сообщении (битый JSON, которое никогда не обработается) — реальный
риск. Минимальная защита: счётчик попыток в памяти, после N неудач — запись в DLQ-топик
(`order.events.v1.dlq`) и коммит. Это следующий шаг, не первый.

### 6.4 Repository / usecase / transport

Структура повторяет order-сервис:

```
services/notification/internal/
    app/app.go
    config/config.go
    domain/
        event.go              # domain.OrderCreated — своя модель, не proto
        inbox.go              # domain.InboxMessage
    repository/pg/
        inbox.go              # Claim
    transport/kafka/
        router.go             # RouterDeps, маршрутизация по event-type
        order.go              # декод protojson → usecase input
    usecase/
        notification.go
```

**Repository — `Claim`, а не `Insert` + `Exists`:**

```go
// Claim пытается застолбить сообщение. false — сообщение уже обработано (дубликат).
func (r *InboxRepo) Claim(ctx context.Context, msg domain.InboxMessage) (bool, error) {
	tag, err := postgres.Exec(ctx, r.pool).Exec(
		ctx,
		`INSERT INTO inbox_messages
			(event_id, topic, partition, msg_offset, event_type, aggregate_id, payload)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (event_id) DO NOTHING`,
		msg.EventID, msg.Topic, msg.Partition, msg.Offset, msg.EventType, msg.AggregateID, msg.Payload,
	)
	if err != nil {
		return false, pgerr.Map(err)
	}
	return tag.RowsAffected() == 1, nil
}
```

Проверка `SELECT ... WHERE event_id = $1` отдельным запросом — гонка: два консьюмера (или один
после ребаланса) могут пройти проверку одновременно. `INSERT ... ON CONFLICT DO NOTHING` +
`RowsAffected` атомарен.

**Usecase — Unit of Work, как в order:**

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

		return uc.notifier.OrderCreated(ctx, input.Event)
	})
}
```

Дедупликация и эффект — в одной транзакции. Если `notifier` упадёт, откатится и запись в inbox,
оффсет не закоммитится, сообщение приедет снова. Ровно то, что нужно.

Пока `notifier.OrderCreated` — это `log.Info` в stdout:

```go
func (n *LogNotifier) OrderCreated(ctx context.Context, e domain.OrderCreated) error {
	n.log.InfoContext(ctx, "order created",
		slog.String("order_id", e.OrderID),
		slog.String("user_id", e.UserID),
		slog.Int64("total_amount", e.TotalAmount),
		slog.Int("items", len(e.Items)),
	)
	return nil
}
```

Отдельным интерфейсом, а не прямым `slog` в usecase, — чтобы потом подменить на письмо/пуш,
не трогая usecase.

**Важная оговорка про эффект вне БД.** Печать в терминал (как и отправка письма) не участвует
в транзакции — на неё транзакционных гарантий нет по определению. Если процесс упадёт между
записью в inbox и самой печатью, транзакция откатится, сообщение переедет снова и печать
произойдёт. Но если упасть **после** commit и до возврата из обработчика — оффсет не
закоммичен, сообщение приедет повторно, а inbox его уже отсекает: печати не будет. Выбор
«эффект до записи в inbox или после» — это выбор между «хотя бы раз» и «не более раза», и
третьего варианта без распределённой транзакции не существует. Здесь выбрано «не более раза»
(inbox первым). Для реальных эффектов вроде письма правильный путь — не менять порядок,
а сделать сам эффект идемпотентным (ключ идемпотентности на стороне провайдера).

**Transport — маршрутизация по типу события:**

```go
func (r *OrderRouter) Handle(ctx context.Context, msg kafkaconsumer.Message) error {
	switch msg.Headers[headerEventType] {
	case domain.EventTypeOrderCreated:
		return r.handleOrderCreated(ctx, msg)
	default:
		// Неизвестный тип — не ошибка: старый консьюмер, новое событие. Пропускаем.
		r.log.Debug("unknown event type", slog.String("type", msg.Headers[headerEventType]))
		return nil
	}
}

func (r *OrderRouter) handleOrderCreated(ctx context.Context, msg kafkaconsumer.Message) error {
	var pb orderv1.OrderCreated
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(msg.Value, &pb); err != nil {
		return apperr.InvalidArgument().Wrap(err)
	}
	...
}
```

`DiscardUnknown: true` — чтобы добавление поля в `OrderCreated` не ломало старых консьюмеров.

Неизвестный `event-type` игнорируем молча (это forward compatibility), а вот битый payload
известного типа — ошибка: значит, контракт нарушен, и это надо увидеть.

### 6.5 app.go

```go
	consumer, err := kafkaconsumer.New(
		cfg.Kafka.Brokers,
		cfg.Kafka.ConsumerGroup,
		cfg.Kafka.Topics,
		kafkaconsumer.WithLogger(log),
	)
	...
	txManager := postgres.NewTxManager(pgPool)
	inboxRepo := repo.NewInboxRepo(pgPool)
	notifier := notify.NewLogNotifier(log)

	notificationUsecase := usecase.NewNotificationUsecase(usecase.NotificationUsecaseDeps{
		Tx:       txManager,
		Inbox:    inboxRepo,
		Notifier: notifier,
		Log:      log,
	})

	router := kafkarouter.NewRouter(kafkarouter.RouterDeps{
		NotificationUsecase: notificationUsecase,
		Log:                 log,
	})
```

`Run` — по образцу order-сервиса, через `errgroup`:

```go
func (a *App) Run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.log.Info("kafka consumer started",
			slog.Any("topics", a.cfg.Kafka.Topics),
			slog.String("group", a.cfg.Kafka.ConsumerGroup))
		defer a.log.Info("kafka consumer stopped")
		return a.consumer.Run(ctx, a.router)
	})

	return g.Wait()
}
```

`Close` сейчас пустой — добавить `a.consumer.Close()` и `a.pgPool.Close()`.

---

## 7. Семантика доставки — сводка

| Участок | Гарантия | Чем обеспечена |
|---|---|---|
| usecase → БД | atomic | одна транзакция на `orders` + `order_items` + `outbox_events` |
| БД → Debezium | at-least-once | позиция в WAL коммитится периодически; после падения Connect перечитает хвост |
| Debezium → Redpanda | at-least-once | ретраи продюсера; при рестарте возможен повтор последних сообщений |
| Redpanda → consumer | at-least-once | ручной коммит оффсета после обработки |
| consumer → эффект | **effectively-once** | inbox: `INSERT ON CONFLICT DO NOTHING` + эффект в одной транзакции |

Дубликаты на каждом шаге — норма, а не авария. Именно поэтому inbox обязателен, а не «на всякий
случай». Exactly-once в транспорте не достигается вообще — достигается идемпотентность эффекта.

**Порядок** гарантирован только внутри партиции. Все события одного заказа имеют один ключ
(`order_id`) → одна партиция → порядок сохранён. Между разными заказами порядка нет и не нужно.

**Про inbox в нашем случае честно:** сейчас эффект — печать в stdout, и дубликат не страшен
(в худшем случае строка напечатается дважды). Inbox тут — отработка паттерна и задел: как только
появится реальный побочный эффект (отправка письма, запись статуса), он станет обязательным.

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

1. **Миграция outbox** в order-сервисе + heartbeat-таблица. `task -d services/order migrate-up`.
2. **`platform/postgres/tx.go`**: `Executor`, `Exec`, `TxManager`. Перевести `OrderRepo.Create`
   на `postgres.Exec(ctx, r.pool)`, транзакцию убрать.
3. **`events.proto`** + `task -d proto generate`.
4. **`internal/event`, `OutboxRepo`, usecase** с `WithinTx`. Проверка: вызвать `CreateOrder`
   через grpcui → в `outbox_events` появилась строка, `payload` — валидный JSON.
   На этом шаге брокера ещё нет вообще, и это нормально.
5. **compose: `wal_level=logical`** для order-postgres → `docker compose up -d` → проверить
   `SHOW wal_level`.
6. **compose: redpanda + console + connect** → `rpk cluster health`, Console на `:8080`,
   `curl localhost:8083/connector-plugins` (должен быть `PostgresConnector`).
7. **Регистрация коннектора** → `task connect-status` (`RUNNING`, без `FAILED` в tasks).
   Создать заказ → сообщение в `order.events.v1`. Смотреть в Console или `rpk topic consume`.
   **До написания консьюмера убедиться, что сообщение в топике правильной формы** — ключ,
   заголовки, payload.
8. **notification-postgres + миграция inbox**, поправить `.env`.
9. **`platform/kafka/kafkaconsumer`** — сначала просто печать в лог того, что приехало, без БД.
10. **domain/repository/usecase/transport** в notification, inbox, Unit of Work.
11. **Проверка дедупликации**: остановить сервис, `rpk group seek notification --to start`,
    запустить → события перечитаются, но в stdout ничего нового (все `event_id` уже в inbox).

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

# Статус коннектора и задач
curl -sS http://localhost:8083/connectors/order-outbox/status | jq

# Логи коннектора (там же трассы падений SMT)
docker logs -f connect

# Топики и сообщения
docker exec -it redpanda rpk topic list
docker exec -it redpanda rpk topic consume order.events.v1 --offset start --format '%k | %h | %v\n'

# Лаг консьюмер-группы
docker exec -it redpanda rpk group describe notification
```

---

## 11. Грабли

1. **`wal_level` не применился.** Меняется только рестартом контейнера; `docker compose restart`
   после правки `command` обязателен. Если контейнер создавался раньше — `docker compose up -d`
   пересоздаст его с новой командой.

2. **Слот репликации не удаляется вместе с коннектором.** `DELETE /connectors/...` удаляет
   коннектор, но слот `order_outbox_slot` остаётся в PostgreSQL и **продолжает держать WAL**.
   Диск растёт при полностью «выключенном» CDC. Удалять руками:
   `SELECT pg_drop_replication_slot('order_outbox_slot');`. В проде — алерт на лаг слота.

3. **Один слот = один коннектор.** Два коннектора с одинаковым `slot.name` подерутся за слот
   (`replication slot is active for PID ...`). Для второй БД — свой `slot.name`.

4. **`decoderbufs` по умолчанию.** Забыть `plugin.name=pgoutput` → коннектор падает на старте
   с ошибкой о недоступном плагине декодирования.

5. **SMT падает на heartbeat-сообщениях**, если не задан `predicate`. Симптом: коннектор
   в `FAILED` с `Unexpected field name` / `can't find payload field`, хотя outbox-сообщения
   корректны.

6. **Первый запуск переотправил все старые события.** Значит `snapshot.mode` остался `initial`
   (дефолт). Для outbox нужен `no_data`.

7. **Консьюмер с хоста не видит брокер.** Симптом: `dial tcp: lookup redpanda: no such host`.
   Причина — advertised-адрес internal listener'а. Сервисы на хосте ходят на `localhost:19092`.

8. **Connect не стартует: `replication factor 3 larger than available brokers`.** Не выставлены
   `*_STORAGE_REPLICATION_FACTOR: 1`.

9. **Заголовки приезжают не в том виде, в каком ожидаешь.** Конвертер заголовков в Connect
   сериализует значения по-своему; строка может приехать с кавычками или без в зависимости
   от версии/настроек. Поэтому `event_id` продублирован внутри payload — на заголовки
   опираемся для маршрутизации, а для дедупликации берём значение из payload.

10. **UPDATE по outbox-таблице.** SMT их только логирует (`table.op.invalid.behavior=warn`) —
    изменение «потеряется» молча. Outbox — append-only, обновлять строки в ней нельзя.

11. **`value` в топике — экранированная строка.** Забыт `table.expand.json.payload=true`
    или `payload` объявлен `TEXT` вместо `jsonb`.

12. **Оффсет закоммичен, а обработка упала.** Проверить, что стоит `DisableAutoCommit()` —
    с автокоммитом franz-go двигает оффсеты по таймеру независимо от обработки.

---

## 12. Осознанно отложено

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
  на неразбираемом сообщении.
* **Чистка outbox/inbox** по расписанию (или partitioning по `created_at`).
* **Трассировка через границу брокера**: Debezium умеет протаскивать span context
  (`tracing.span.context.field` в SMT) — вернуться к этому вместе с observability из TODO.
* **Идемпотентность на входе `CreateOrder`** (ключ идемпотентности в gRPC) — отдельная задача,
  уже есть в TODO. Outbox/inbox её не заменяют: они защищают доставку события, а не повторный
  вызов API.
