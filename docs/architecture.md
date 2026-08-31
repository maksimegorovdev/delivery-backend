# Delivery Platform — Архитектура

Учебный проект: 4 микросервиса на Go для отработки навыков работы с БД, транзакциями,
Kafka, паттернами **outbox** и **inbox**.

Документ описывает: схему взаимодействия сервисов, контракты gRPC/HTTP, схемы БД,
границы транзакций и универсальную Clean Architecture для всех сервисов.

---

## 1. Состав системы

| Сервис | Транспорт наружу | БД | Kafka | Роль |
|---|---|---|---|---|
| **gateway** | HTTP (REST) | — | — | Единая точка входа, HTTP→gRPC, fake-auth middleware, request_id |
| **user** | gRPC | `userdb` (5433) | — | Хранит пользователей. Пока только `GetUser`, один seed-пользователь |
| **order** | gRPC | `orderdb` (5432) | producer (outbox) | Создание заказов, источник событий `order.*` |
| **notification** | — (только воркер) | `notificationdb` (5434) | consumer (inbox) | Слушает события, пишет «уведомление» в лог + в таблицу |

Общие модули:

- `platform` — общие пакеты (logger, postgres, grpcserver, httpserver, apperr, txmanager, outbox, inbox, kafka, …).
- `proto` — контракты gRPC (`user/v1`, `order/v1`, далее `v2`).

### Что осознанно упрощено на текущем этапе

- **Нет `CreateUser`.** Пользователь заводится миграцией-сидом с фиксированным UUID.
  Этот же UUID прибит в fake-auth middleware gateway.
- **Нет авторизации.** Gateway middleware кладёт `user_id` в контекст (fake-auth).
  Когда появится реальный auth — меняется только middleware, контракты не трогаются.
- **Только рубли.** Поля денег — целое число копеек (`int64` / `BIGINT`). Никакого `currency`.
- **Notification слушает только `order.events.v1`.** Поток `user.*` (outbox в user-сервисе)
  добавляется позже как симметричное упражнение — скелет не меняется.

---

## 2. Схема взаимодействия

```
                 ┌───────────┐
   HTTP client ─▶ │  gateway  │
                 └─────┬─────┘
        gRPC (CreateOrder / GetOrder)     gRPC (GetUser)
                 ┌─────▼─────┐            ┌─────▼─────┐
                 │   order   │──────────▶ │   user    │   order валидирует user_id
                 │           │   gRPC     │           │   (существование + status)
                 │ orders     │            │ users     │
                 │ order_items│            └───────────┘
                 │ outbox     │
                 └─────┬─────┘
      outbox publisher │  (goroutine в App.Run, errgroup)
                       ▼
             ┌──────────────── Kafka ────────────────┐
             │   topic: order.events.v1              │
             │   key = order_id, partitions 3        │
             └──────────────────┬───────────────────┘
                                │ consumer group "notification.order"
                     ┌──────────▼──────────┐
                     │    notification      │
                     │  inbox  (dedup)      │
                     │  notifications (лог) │
                     │  → slog.Info(...)    │
                     └─────────────────────┘
```

### Sequence: создание заказа

```
client → gateway     POST /v1/orders { items, delivery_address }   (+ header X-User-Id)
gateway              middleware fake-auth: user_id → context
gateway → order      gRPC CreateOrder{ user_id, items, delivery_address }   (request_id в metadata)

order → user         gRPC GetUser{ id: user_id }
                     проверка: пользователь существует и status == active
                     (внешний вызов ДО открытия транзакции)

order  (1 транзакция) BEGIN
                        INSERT orders (...)           status = 'pending'
                        INSERT order_items (...)      (батч)
                        INSERT outbox (event_type='order.created', payload=envelope)
                      COMMIT

order → gateway      CreateOrderResponse{ order }
gateway → client     201 { order_id, status, total_amount, ... }

--- асинхронно ---

order.publisher      тикер ~1s:
                       BEGIN
                         SELECT * FROM outbox
                           WHERE published_at IS NULL
                           ORDER BY id
                           FOR UPDATE SKIP LOCKED
                           LIMIT 100
                         produce → Kafka  (key = aggregate_id, acks=all)
                         UPDATE outbox SET published_at = now() WHERE id = ANY(...)
                       COMMIT

notification.consumer читает сообщение из order.events.v1
                       BEGIN
                         INSERT INTO inbox (event_id, ...) ON CONFLICT (event_id) DO NOTHING
                         -- если вставилось 0 строк → событие уже обработано → COMMIT, ack, выход
                         INSERT INTO notifications (...)
                       COMMIT
                       slog.Info("order accepted notification", ...)   -- сам "эффект"
                       commit offset (kafka)
```

---

## 3. Границы транзакций

| # | Где | Одна транзакция охватывает | Зачем |
|---|---|---|---|
| 1 | `order.CreateOrder` | `INSERT orders` + `INSERT order_items` + `INSERT outbox(order.created)` | Заказ и факт события атомарны: событие не теряется при падении после коммита |
| 2 | `order.publisher` | `SELECT ... FOR UPDATE SKIP LOCKED` → `produce` → `UPDATE published_at` | Строки залочены на время публикации. Падение после `produce`, но до `COMMIT` → повторная публикация (дубликат) → гасится inbox |
| 3 | `notification.consumer` | `INSERT inbox ON CONFLICT DO NOTHING` + `INSERT notifications` | Идемпотентность: at-least-once доставка, один `event_id` не обрабатывается дважды. Offset коммитится только после `COMMIT` |

Валидация `user_id` через `user.GetUser` — **до** транзакции: внешний вызов внутри
транзакции держит соединение и блокировки, это антипаттерн.

### Гарантии

- **Доставка:** at-least-once.
- **Порядок:** в пределах одного агрегата (партиционирование по `order_id`;
  `uuidv7` в outbox даёт возрастающие `id`, публикуем `ORDER BY id`).
  Глобального порядка между заказами нет и не требуется.
- **Идемпотентность консьюмера:** таблица `inbox`, PK = `event_id`.

### Сценарии отказов для ручной проверки

1. Убить Kafka в момент публикации → события копятся в `outbox`, после подъёма — досылаются.
2. Перезапустить `notification` с уже прочитанным, но не закоммиченным offset →
   повторная доставка → `inbox` гасит дубликат.
3. Уронить `notification` между `COMMIT` транзакции и `commit offset` → то же самое.
4. Поставить seed-пользователю `status = 'banned'` → `CreateOrder` отвечает
   `FailedPrecondition`, gateway — 409/422.
5. Остановить `user` → `CreateOrder` отвечает `Unavailable`, gateway — 503.

---

## 4. Контракты

### 4.1 proto (`proto/`)

Раскладка:

```
proto/
  buf.yaml
  buf.gen.yaml
  user/v1/user.proto        # package user.v1  → gen/user/v1
  order/v1/order.proto       # package order.v1 → gen/order/v1
  # далее при необходимости: order/v2/order.proto
  gen/                       # сгенерированный код (в .gitignore или коммитим — на выбор)
```

#### `user/v1/user.proto`

```proto
syntax = "proto3";
package user.v1;
option go_package = "github.com/maksimegorovdev/delivery-backend/proto/gen/user/v1;userv1";

import "google/protobuf/timestamp.proto";

service UserService {
  rpc GetUser (GetUserRequest) returns (GetUserResponse);
}

message User {
  string id = 1;
  string email = 2;
  string first_name = 3;
  string last_name = 4;
  UserStatus status = 5;
  google.protobuf.Timestamp created_at = 6;
}

enum UserStatus {
  USER_STATUS_UNSPECIFIED = 0;
  USER_STATUS_PENDING = 1;
  USER_STATUS_ACTIVE = 2;
  USER_STATUS_BANNED = 3;
}

message GetUserRequest { string id = 1; }
message GetUserResponse { User user = 1; }
```

#### `order/v1/order.proto`

```proto
syntax = "proto3";
package order.v1;
option go_package = "github.com/maksimegorovdev/delivery-backend/proto/gen/order/v1;orderv1";

import "google/protobuf/timestamp.proto";

service OrderService {
  rpc CreateOrder (CreateOrderRequest) returns (CreateOrderResponse);
  rpc GetOrder    (GetOrderRequest)    returns (GetOrderResponse);
}

message OrderItem {
  string product_id = 1;
  string name = 2;
  int32  quantity = 3;
  int64  unit_price = 4;   // копейки
}

enum OrderStatus {
  ORDER_STATUS_UNSPECIFIED = 0;
  ORDER_STATUS_PENDING = 1;
  ORDER_STATUS_CONFIRMED = 2;
  ORDER_STATUS_CANCELLED = 3;
}

message Order {
  string id = 1;
  string user_id = 2;
  OrderStatus status = 3;
  repeated OrderItem items = 4;
  int64  total_amount = 5;            // копейки
  string delivery_address = 6;
  google.protobuf.Timestamp created_at = 7;
}

message CreateOrderRequest {
  string user_id = 1;                 // из fake-auth контекста gateway
  repeated OrderItem items = 2;
  string delivery_address = 3;
}
message CreateOrderResponse { Order order = 1; }

message GetOrderRequest { string id = 1; }
message GetOrderResponse { Order order = 1; }
```

Разделение ответственности в gRPC:

- **бизнес-идентификаторы** (`user_id`) — в теле сообщения;
- **сквозное** (`request_id`, позже `trace_id`, токен) — в gRPC metadata через interceptor'ы.

### 4.2 HTTP API (gateway)

```
GET  /v1/users/{id}        → user.GetUser
POST /v1/orders            → order.CreateOrder   (user_id из контекста, НЕ из тела)
GET  /v1/orders/{id}       → order.GetOrder
```

Формат ошибки (единый):

```json
{ "error": { "code": "user_not_active", "message": "user is not active" } }
```

### 4.3 События Kafka

#### Топики

| Топик | Ключ | Producer | Consumer group | Партиций |
|---|---|---|---|---|
| `order.events.v1` | `order_id` | order (outbox publisher) | `notification.order` | 3 |
| `order.events.v1.dlq` | — | notification | (ручной разбор) | 1 |

#### Конверт события (единый для всех типов)

```json
{
  "event_id":       "0192f2b1-....",       // = outbox.id, идемпотентный ключ для inbox
  "event_type":     "order.created",
  "occurred_at":    "2026-08-30T12:00:00Z",
  "aggregate_type": "order",
  "aggregate_id":   "0192a0c4-....",
  "trace_id":       "req-abc123",           // из request_id
  "payload": {
    "order_id":         "0192a0c4-....",
    "user_id":          "0191b7d9-....",
    "total_amount":     125000,             // копейки
    "delivery_address": "г. Москва, ...",
    "items": [
      { "product_id": "...", "name": "...", "quantity": 2, "unit_price": 62500 }
    ],
    "created_at":       "2026-08-30T12:00:00Z"
  }
}
```

Kafka headers дублируют `event_id`, `event_type`, `trace_id` — чтобы фильтровать и
дедуплицировать без разбора тела.

#### Типы событий

- `order.created` — payload как выше.
- (позже) `order.status_changed` — для второго шага/саги.
- (позже) `user.registered` — когда появится `CreateUser` + outbox в user-сервисе.

#### Про «тонкое» событие

`order.created` несёт `user_id`, но НЕ email/имя. `notification` обогащает данные
синхронным вызовом `user.GetUser`. Плюсы: нет устаревших данных в событии, тренируется
gRPC-вызов внутри консьюмера. Минус: лишний вызов — для учебного проекта приемлемо.

---

## 5. Схемы БД

Одна БД на сервис. Миграции — `golang-migrate`, каталог `services/<svc>/migrations/pg`.

### 5.1 `userdb`

```sql
-- 000001_create_users_table.up.sql  (уже есть)
CREATE TABLE IF NOT EXISTS users (
    id         UUID PRIMARY KEY DEFAULT uuidv7(),
    email      TEXT NOT NULL,
    first_name TEXT NOT NULL,
    last_name  TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'active', 'banned')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ DEFAULT NULL
);

CREATE UNIQUE INDEX idx_users_email ON users (email) WHERE deleted_at IS NULL;
```

```sql
-- 000002_seed_user.up.sql
INSERT INTO users (id, email, first_name, last_name, status)
VALUES ('00000000-0000-7000-8000-000000000001',
        'demo@delivery.local', 'Demo', 'User', 'active')
ON CONFLICT DO NOTHING;
```
```sql
-- 000002_seed_user.down.sql
DELETE FROM users WHERE id = '00000000-0000-7000-8000-000000000001';
```

Этот UUID — константа в конфиге gateway (`FAKE_AUTH_USER_ID`).

### 5.2 `orderdb`

```sql
-- 000001_create_orders_tables.up.sql
CREATE TABLE orders (
    id               UUID PRIMARY KEY DEFAULT uuidv7(),
    user_id          UUID        NOT NULL,
    status           TEXT        NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'confirmed', 'cancelled')),
    total_amount     BIGINT      NOT NULL CHECK (total_amount >= 0),   -- копейки
    delivery_address TEXT        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_orders_user_id ON orders (user_id);

CREATE TABLE order_items (
    id         UUID PRIMARY KEY DEFAULT uuidv7(),
    order_id   UUID   NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    product_id UUID   NOT NULL,
    name       TEXT   NOT NULL,
    quantity   INT    NOT NULL CHECK (quantity > 0),
    unit_price BIGINT NOT NULL CHECK (unit_price >= 0)                 -- копейки
);
CREATE INDEX idx_order_items_order_id ON order_items (order_id);
```

```sql
-- 000002_create_outbox_table.up.sql
CREATE TABLE outbox (
    id             UUID PRIMARY KEY DEFAULT uuidv7(),
    aggregate_type TEXT        NOT NULL,
    aggregate_id   UUID        NOT NULL,
    event_type     TEXT        NOT NULL,
    payload        JSONB       NOT NULL,
    headers        JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at   TIMESTAMPTZ,
    attempts       INT         NOT NULL DEFAULT 0,
    last_error     TEXT
);

-- publisher видит только неопубликованные строки
CREATE INDEX idx_outbox_unpublished ON outbox (id) WHERE published_at IS NULL;
```

### 5.3 `notificationdb`

```sql
-- 000001_create_inbox_notifications.up.sql
CREATE TABLE inbox (
    event_id     UUID PRIMARY KEY,            -- дедуп: тот же event_id не обработается дважды
    event_type   TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ
);

CREATE TABLE notifications (
    id         UUID PRIMARY KEY DEFAULT uuidv7(),
    event_id   UUID        NOT NULL REFERENCES inbox (event_id),
    user_id    UUID        NOT NULL,
    kind       TEXT        NOT NULL,           -- 'order_created' | ...
    channel    TEXT        NOT NULL DEFAULT 'log',
    message    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_notifications_user_id ON notifications (user_id);
```

Сам «эффект» (запись в лог) не транзакционен — это нормально для учебного проекта.
Важно, что `inbox + notifications` пишутся атомарно, а offset коммитится только после `COMMIT`.

### 5.4 docker-compose

Добавить сервис `notification-postgres` на порт `5434`, БД `notificationdb`,
и Kafka (single-node KRaft) + опционально kafka-ui / `kcat` для отладки.
В `.env` notification-сервиса поправить `PG_DSN` на `.../notificationdb`.

---

## 6. Универсальная Clean Architecture

Одна и та же структура слоёв во всех сервисах. Транспорт (gRPC/HTTP) и версия API —
детали **inbound-адаптера**; `domain` и `usecase` от них не зависят.

### 6.1 Правило зависимостей

```
adapters ──▶ usecase ──▶ domain
   │            │
   └── ports ◀──┘     (интерфейсы объявлены для usecase, реализованы в adapters/outbound)
```

- **domain** — сущности, value objects, доменные ошибки, инварианты.
  Ноль тегов, ноль внешних типов (нет protobuf / json / pgx / chi).
- **usecase** — оркестрация, границы транзакций, вызовы зависимостей через `ports`.
  Не импортирует proto / json / pgx / chi.
- **ports** — интерфейсы репозиториев, внешних клиентов, publisher'ов.
- **adapters/inbound** — транспорт, версионирование, DTO ↔ команда/сущность.
- **adapters/outbound** — репозитории (pgx), gRPC-клиенты, Kafka. Реализуют `ports`.
- **app** — composition root, вся проводка зависимостей.

### 6.2 Представления одной сущности

Для `Order` — 3 обязательных представления + 1 опциональное:

| Слой | Тип | Теги / типы | Где живёт |
|---|---|---|---|
| Wire DTO (per transport, per version) | `orderv1.CreateOrderRequest` / `httpv1.CreateOrderRequestDTO` | protobuf / `json:` + ozzo-validation | `adapters/inbound/{grpc,http}/vN` |
| Команда usecase | `usecase.CreateOrderCommand` | без тегов, примитивы / VO | `usecase` |
| Доменная сущность | `domain.Order` | без тегов, VO (`Money`, `OrderStatus`) | `domain` |
| DB row | `orderRow` | `pgtype.*` / `sql.Null*` | `adapters/outbound/pgrepo` |

На выход из usecase возвращаем **доменную сущность** (у неё нет тегов — это безопасно);
inbound-адаптер маппит её в wire DTO. Отдельный `Output`-DTO вводим только если нужно
вернуть то, чего нет в домене (агрегаты, счётчики).

### 6.3 Поток маппинга (создание заказа)

```
gRPC:  orderv1.CreateOrderRequest
         → grpc/v1 mapper.toCommand()      → usecase.CreateOrderCommand
         → usecase.CreateOrder(ctx, cmd)   → (*domain.Order, error)
         → grpc/v1 mapper.toProto(order)   → orderv1.Order

HTTP:  httpv1.CreateOrderRequestDTO  (+ .Validate())
         → http/v1 mapper.toCommand()      → usecase.CreateOrderCommand   (та же команда!)
         → usecase.CreateOrder(ctx, cmd)   → (*domain.Order, error)
         → http/v1 mapper.toResponse(o)    → httpv1.OrderResponseDTO
```

Один usecase, N версий транспорта, версионные мапперы.

### 6.4 Скетчи кода

**domain** (`internal/domain/order.go`):

```go
type Money int64 // копейки

type OrderStatus int

const (
    OrderStatusPending OrderStatus = iota + 1
    OrderStatusConfirmed
    OrderStatusCancelled
)

func ParseOrderStatus(s string) (OrderStatus, error) { /* map + ErrInvalidOrderStatus */ }
func (s OrderStatus) String() string                 { /* обратный map */ }

type OrderItem struct {
    ProductID uuid.UUID
    Name      string
    Quantity  int
    UnitPrice Money
}

type Order struct {
    ID              uuid.UUID
    UserID          uuid.UUID
    Status          OrderStatus
    Items           []OrderItem
    TotalAmount     Money
    DeliveryAddress string
    CreatedAt       time.Time
}

// конструктор enforce-ит инварианты домена
func NewOrder(userID uuid.UUID, items []OrderItem, address string) (*Order, error) {
    if len(items) == 0 {
        return nil, ErrEmptyOrder
    }
    if strings.TrimSpace(address) == "" {
        return nil, ErrEmptyAddress
    }
    var total Money
    for _, it := range items {
        if it.Quantity <= 0 || it.UnitPrice < 0 {
            return nil, ErrInvalidItem
        }
        total += it.UnitPrice * Money(it.Quantity)
    }
    return &Order{
        ID:              uuid.Must(uuid.NewV7()),
        UserID:          userID,
        Status:          OrderStatusPending,
        Items:           items,
        TotalAmount:     total,
        DeliveryAddress: address,
        CreatedAt:       time.Now().UTC(),
    }, nil
}
```

**usecase** (`internal/usecase/create_order.go`):

```go
type CreateOrderItem struct {
    ProductID uuid.UUID
    Name      string
    Quantity  int
    UnitPrice int64
}

type CreateOrderCommand struct {
    UserID  uuid.UUID
    Items   []CreateOrderItem
    Address string
}

type OrderUseCase struct {
    tx     txmanager.TxManager
    orders ports.OrderRepository
    outbox ports.OutboxWriter
    users  ports.UserClient
}

func (uc *OrderUseCase) CreateOrder(ctx context.Context, cmd CreateOrderCommand) (*domain.Order, error) {
    // 1. синхронная валидация пользователя — ДО транзакции
    u, err := uc.users.GetUser(ctx, cmd.UserID)
    if err != nil {
        return nil, err // ErrUserNotFound / Unavailable уже классифицированы в userclient
    }
    if u.Status != domain.UserStatusActive {
        return nil, domain.ErrUserNotActive
    }

    // 2. построение доменной сущности (инварианты внутри)
    order, err := domain.NewOrder(cmd.UserID, toDomainItems(cmd.Items), cmd.Address)
    if err != nil {
        return nil, err
    }

    // 3. одна транзакция: заказ + позиции + outbox
    err = uc.tx.Do(ctx, func(ctx context.Context) error {
        if err := uc.orders.Save(ctx, order); err != nil {
            return err
        }
        return uc.outbox.Write(ctx, outbox.Message{
            AggregateType: "order",
            AggregateID:   order.ID,
            EventType:     "order.created",
            Payload:       buildOrderCreatedPayload(order),
        })
    })
    if err != nil {
        return nil, err
    }
    return order, nil
}
```

**ports** (`internal/ports/ports.go`):

```go
type OrderRepository interface {
    Save(ctx context.Context, o *domain.Order) error
    ByID(ctx context.Context, id uuid.UUID) (*domain.Order, error)
}

type OutboxWriter interface {
    Write(ctx context.Context, msg outbox.Message) error
}

type UserClient interface {
    GetUser(ctx context.Context, id uuid.UUID) (*domain.User, error)
}
```

**adapters/outbound/pgrepo** — row + маппинг:

```go
type orderRow struct {
    ID              uuid.UUID
    UserID          uuid.UUID
    Status          string
    TotalAmount     int64
    DeliveryAddress string
    CreatedAt       time.Time
}

func (r orderRow) toDomain(items []domain.OrderItem) (*domain.Order, error) {
    st, err := domain.ParseOrderStatus(r.Status)
    if err != nil {
        return nil, err
    }
    return &domain.Order{
        ID:              r.ID,
        UserID:          r.UserID,
        Status:          st,
        Items:           items,
        TotalAmount:     domain.Money(r.TotalAmount),
        DeliveryAddress: r.DeliveryAddress,
        CreatedAt:       r.CreatedAt,
    }, nil
}

// nullable-поле: row → domain
type userRow struct {
    ID    uuid.UUID
    Email string
    Phone pgtype.Text
}

func (r userRow) toDomain() domain.User {
    u := domain.User{ID: r.ID, Email: r.Email}
    if r.Phone.Valid {
        p := r.Phone.String
        u.Phone = &p
    }
    return u
}
```

Правила маппинга:

- **Из БД в домен — всегда явно.** Не сканировать напрямую в `domain.Order`: домен
  использует VO (`Money`, `OrderStatus`), в `toDomain` парсится статус-строка в enum
  и разбираются `NULL`. Изменение схемы не течёт в домен.
- Для `INSERT` полноценный row-struct не нужен — передавай поля в `Exec` напрямую.
  Row-struct нужен под `SELECT` / `Scan`.
- Обратное направление (`fromDomain`) заводи только если так удобнее собирать
  аргументы запроса.

### 6.5 Где какая валидация

| Вид | Где | Ошибка на выходе |
|---|---|---|
| Синтаксис (required, формат UUID, длины, `quantity > 0`) | DTO во inbound-адаптере (ozzo-validation) | `InvalidArgument` / 400 |
| Инварианты домена (≥ 1 позиция, сумма ≥ 0, непустой адрес) | конструктор `domain.NewOrder` | доменная ошибка |
| Бизнес-правила с внешними данными (`user.status == active`) | usecase | доменная ошибка |

### 6.6 Модель ошибок

`platform/apperr` — стабильный примитив, допускается импорт даже в `domain`:

```go
type Kind int

const (
    KindInternal Kind = iota
    KindNotFound
    KindInvalid
    KindConflict
    KindFailedPrecondition
    KindUnauthenticated
)

type Error struct {
    Kind    Kind
    Code    string // машиночитаемый: "order_not_found"
    Message string
    cause   error
}

func NotFound(code, msg string) *Error          { /* ... */ }
func Invalid(code, msg string) *Error           { /* ... */ }
func FailedPrecondition(code, msg string) *Error { /* ... */ }
func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }
```

`domain/errors.go`:

```go
var (
    ErrOrderNotFound = apperr.NotFound("order_not_found", "order not found")
    ErrUserNotFound  = apperr.NotFound("user_not_found", "user not found")
    ErrUserNotActive = apperr.FailedPrecondition("user_not_active", "user is not active")
    ErrEmptyOrder    = apperr.Invalid("empty_order", "order must contain at least one item")
    ErrEmptyAddress  = apperr.Invalid("empty_address", "delivery address is required")
    ErrInvalidItem   = apperr.Invalid("invalid_item", "order item is invalid")
)
```

Маппинг в транспортный код — **один раз** в interceptor / middleware, не в каждом хендлере:

- gRPC error-interceptor: `apperr.Kind` → `codes.NotFound` / `codes.InvalidArgument` /
  `codes.FailedPrecondition` / `codes.Internal`.
- HTTP error-middleware: `apperr.Kind` → HTTP-статус + тело `{"error":{"code","message"}}`.
- Неизвестная ошибка (не `*apperr.Error`) → `Internal` / 500, детали только в лог.

### 6.7 Версионирование API (v1 / v2)

Версия — забота **только inbound-адаптера**. `domain` и `usecase` не версионируются.

**proto:**

```
proto/order/v1/order.proto     package order.v1  → gen/order/v1
proto/order/v2/order.proto     package order.v2  → gen/order/v2
```

gRPC-сервер регистрирует оба сервиса одновременно; оба хендлера зовут один `OrderUseCase`.

**HTTP:**

```
adapters/inbound/http/
  router.go            // r.Mount("/v1", v1.Routes(uc)); r.Mount("/v2", v2.Routes(uc))
  v1/{handler.go, dto.go, mapper.go}
  v2/{handler.go, dto.go, mapper.go}   // свои DTO, свой mapper, тот же uc
```

Если v2 требует другого поведения — новый метод usecase (`CreateOrderV2`) или параметр
в команде. Никаких `if version == 2` в домене. Если расхождение слишком большое —
форк usecase, домен общий.

### 6.8 Каноничное дерево сервиса (order)

```
services/order/
  cmd/main.go
  migrations/pg/
  internal/
    app/app.go                     # composition root: config→logger→pg→txmanager→repos→
                                   #   userclient→usecase→grpc(v1[,v2])→outbox.Publisher; errgroup
    config/config.go
    domain/
      order.go   user.go   errors.go
    usecase/
      order_uc.go   create_order.go   get_order.go   dto.go   # CreateOrderCommand и пр.
    ports/
      ports.go                     # OrderRepository, OutboxWriter, UserClient
    adapters/
      inbound/
        grpc/
          v1/{handler.go, mapper.go}
          # v2/{handler.go, mapper.go}
      outbound/
        pgrepo/
          order_repo.go            # orderRow + toDomain
          outbox_repo.go           # реализация OutboxWriter поверх platform/outbox
        userclient/
          client.go                # обёртка над userv1.UserServiceClient → domain.User + apperr
        kafka/
          producer.go              # Produce для outbox.Publisher
```

Вариации:

- **user** — то же дерево, минус `outbox`, `kafka`, `userclient`; inbound только gRPC
  (`GetUser`); usecase тонкий.
- **gateway** — inbound только HTTP (`http/v1[, v2]`); outbound — `grpcclient` к order и
  user; `domain` может отсутствовать, `usecase` = «оркестрация вызовов».
- **notification** — inbound = Kafka consumer (`adapters/inbound/kafka`); outbound —
  `pgrepo` (inbox + notifications) и `userclient` (обогащение); usecase = обработчик события.

### 6.9 Воркеры и graceful shutdown

`outbox.Publisher` (order) и Kafka consumer (notification) запускаются в том же
`errgroup` в `App.Run`, что и серверы, и завершаются по `ctx.Done()` — как сейчас
сделано с gRPC-сервером.

---

## 7. Пакеты `platform`, которые нужно добавить

| Пакет | Назначение |
|---|---|
| `platform/apperr` | `Kind` + конструкторы ошибок (см. 6.6) |
| `platform/txmanager` | `Do(ctx, func(ctx) error) error`; кладёт `pgx.Tx` в контекст, репозитории берут executor из контекста (tx) либо из pool. Позволяет usecase объединить несколько репозиториев в одну транзакцию, не протаскивая `pgx.Tx` в сигнатуры |
| `platform/outbox` | `Message`, `Writer.Write(ctx, msg)` — вставка в `outbox` внутри текущей транзакции; `Publisher` — воркер: тикер + `FOR UPDATE SKIP LOCKED` + `Produce` + пометка `published_at`, инкремент `attempts` / `last_error` при ошибке |
| `platform/inbox` | `Process(ctx, tx, event, handler)` — `INSERT ... ON CONFLICT (event_id) DO NOTHING`; если 0 строк → событие уже обработано, `handler` не вызывается |
| `platform/kafka` | Обёртки producer / consumer. Рекомендация: `segmentio/kafka-go` — явные consumer groups и ручной `CommitMessages`. Consumer-цикл: `FetchMessage` → `handler` → `CommitMessages` |
| `platform/grpc/grpcclient` | Dial с дефолтами (timeout, keepalive, retry) + outgoing-interceptor: проброс `request_id` в metadata |
| `platform/grpc/interceptors` | server: recover, logging, request_id (extract), **error-map** (`apperr`→`codes`); client: request_id (inject) |
| `platform/httpserver` middleware | `RequestID`, `Logger`, `Recover`, **ErrorMap** (`apperr`→status), `FakeAuth` |

### fake-auth middleware (gateway)

```go
func FakeAuth(defaultUserID string) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            uid := r.Header.Get("X-User-Id")
            if uid == "" {
                uid = defaultUserID // FAKE_AUTH_USER_ID из конфига = seed UUID
            }
            ctx := appctx.WithUserID(r.Context(), uid)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

`platform/appctx` расширить парой `WithUserID` / `UserID` (рядом с существующим request_id).
Хендлер gateway достаёт `appctx.UserID(ctx)` и кладёт в `CreateOrderRequest.UserId`.

---

## 8. Порядок реализации

Отражает и уточняет `TODO.md`.

1. **proto**: `buf` + генерация, `user/v1`, `order/v1`. Подключить `gen` в `go.work`.
2. **platform**: `apperr`, `grpcclient`, `interceptors`, http-middleware
   (request_id, logger, recover, error-map, fake-auth). `appctx.WithUserID`.
3. **user-service** end-to-end: `domain` → `pgrepo` (`userRow.toDomain`) → `usecase`
   (`GetUser`) → `grpc/v1`. Миграция-сид пользователя.
4. **gateway**: HTTP `v1` → `grpcclient` к user. Проверить `GET /v1/users/{id}`.
5. **platform/txmanager** + **platform/outbox** (`Writer`). Миграция `outbox` в `orderdb`.
6. **order-service** end-to-end: `CreateOrder` с транзакцией `orders + order_items +
   outbox`, синхронная валидация через `userclient`. `GetOrder`. Подключить в gateway
   `POST /v1/orders`, `GET /v1/orders/{id}`.
7. **platform/kafka** (producer) + `outbox.Publisher`. Поднять Kafka в compose.
   Проверить события в `order.events.v1` через `kcat -C`.
8. **platform/inbox** + `platform/kafka` (consumer). **notification-service**:
   consumer-цикл → транзакция `inbox + notifications` → `slog.Info`. Добавить
   `notification-postgres` в compose, поправить `.env`.
9. Обогащение: `notification` → `user.GetUser` для имени/email в тексте уведомления.
10. Ручная проверка сценариев отказов (раздел 3).
11. Тесты: unit (usecase с моками `ports`), интеграционные (testcontainers: pg + kafka),
    e2e через gateway.
12. Доработки: DLQ (`order.events.v1.dlq`) после N `attempts`, backoff в publisher,
    observability (трейсы/метрики), таймауты и лимиты соединений в пакетах.

---

## 9. Debezium — опционально, позже

Таблица `outbox` совместима с Debezium Outbox Event Router. Когда дойдёшь:
убираешь свой `outbox.Publisher`, поднимаешь Kafka Connect + Debezium на WAL `orderdb`,
SMT мапит строки `outbox` → топики. Плюс: нет кода публикации и нет `produce` внутри
транзакции. Минус: тяжелее инфраструктура.

Рекомендуемый порядок для учёбы: сначала свой polling-publisher (в нём вся суть
паттерна и практика `FOR UPDATE SKIP LOCKED`), затем для сравнения — Debezium.
