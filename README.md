# Delivery Platform

Тренировочный проект на Go: три небольших сервиса (`order-service`, `user-service`,
`notification-service`), на которых практикуются pgx, транзакции, outbox-паттерн,
Kafka, gRPC и concurrency-паттерны Go.

Этот файл — справочник по архитектуре, контрактам и моделям данных. Код пишется
руками, без генерации ИИ — здесь только то, о чём нужно договориться заранее,
чтобы не путаться в процессе.

## Сервисы и зона ответственности

| Сервис | Владеет данными | Протоколы |
|---|---|---|
| `order-service` | `orders`, `order_items`, `outbox_events` | HTTP (клиент), gRPC-клиент к user-service, Kafka producer |
| `user-service` | `users` | gRPC-сервер |
| `notification-service` | `processed_events` (dedup) | Kafka consumer |

У каждого сервиса **своя база данных** — никаких общих таблиц и cross-database
foreign key (их и не бывает физически). Связь между сервисами — только через
контракты ниже.

## Схема взаимодействия

### Синхронная часть (внутри одного HTTP-запроса)

```
Client
  │ POST /orders {user_id, items}
  ▼
order-service: validate payload
  ▼
order-service --gRPC--> user-service: GetUser(user_id)
  │  (вызывается ДО открытия транзакции — не держим TX открытой на время сети)
  │  user-service не найден/недоступен → 404/502 клиенту, транзакция не начата
  ▼
order-service: BEGIN TX
  ├── INSERT orders
  ├── INSERT order_items
  └── INSERT outbox_events (event_type='order.created')
  COMMIT
  ▼
order-service --> 201 Created клиенту
```

### Асинхронная часть (фон, вне запроса)

```
outbox worker (горутина в order-service, отдельный poll-loop)
  │ SELECT * FROM outbox_events
  │   WHERE status='pending'
  │   ORDER BY created_at LIMIT 100
  │   FOR UPDATE SKIP LOCKED         -- защита от дублей при N инстансах воркера
  ▼
Kafka producer.Publish(topic="order.events", key=order_id, value=envelope)
  ├── успех  → UPDATE outbox_events SET status='published'
  └── ошибка → оставить 'pending', retry на следующем тике (событие не теряется)
  ▼
Kafka topic "order.events" (партиция по order_id → порядок событий одного заказа сохраняется)
  ▼
notification-service: consumer group
  │ 1. INSERT INTO processed_events (event_id) ON CONFLICT DO NOTHING
  │    0 rows affected → уже обработано, скип (дедуп at-least-once → effectively-once)
  │ 2. новое событие → в channel
  ▼
worker pool (N горутин) разбирают channel → send notification
  ▼
commit Kafka offset ТОЛЬКО после успешной обработки воркером
```

### Что происходит при отказах

- **user-service лежит** → ошибка клиенту до начала транзакции, ничего не портится в БД.
- **Kafka лежит** → заказ всё равно создан и закоммичен, outbox копит `pending` и ретраит при восстановлении Kafka — в этом весь смысл outbox-паттерна.
- **notification-service падает во время обработки** → offset не закоммичен → сообщение придёт снова при рестарте → dedup по `event_id` спасает от повторной отправки.

## Модели данных

### user-service

```sql
CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(), -- рассмотреть UUIDv7
    email       TEXT NOT NULL UNIQUE,
    full_name   TEXT NOT NULL,
    active      BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### order-service

```sql
CREATE TABLE orders (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL,
    status       TEXT NOT NULL DEFAULT 'created'
                 CHECK (status IN ('created','paid','cancelled')),
    total_amount NUMERIC(12,2) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE order_items (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id   UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id UUID NOT NULL,
    quantity   INT NOT NULL CHECK (quantity > 0),
    price      NUMERIC(12,2) NOT NULL -- снапшот цены на момент заказа, не ссылка на каталог
);
CREATE INDEX idx_order_items_order_id ON order_items(order_id);
-- ^ FK не индексируется автоматически в Postgres (в отличие от PRIMARY KEY) —
--   индекс нужно создавать руками для стороны, которая ссылается.

CREATE TABLE outbox_events (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type   TEXT NOT NULL,
    payload      JSONB NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','published')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);
CREATE INDEX idx_outbox_pending ON outbox_events (created_at) WHERE status = 'pending';
```

### notification-service

```sql
CREATE TABLE processed_events (
    event_id     UUID PRIMARY KEY,
    processed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## Контракты

### 1. HTTP API (order-service)

```
POST /orders
Request:
{
  "user_id": "uuid",
  "items": [
    { "product_id": "uuid", "quantity": 2, "price": "19.99" }
  ]
}

201 Response:
{
  "order_id": "uuid",
  "status": "created",
  "total_amount": "39.98",
  "created_at": "2026-08-25T10:00:00Z"
}

Ошибки:
400 — невалидный payload
404 — user_id не найден
502 — user-service недоступен/timeout
500 — внутренняя ошибка
```

### 2. gRPC (`proto/user/v1/user.proto`)

```protobuf
syntax = "proto3";
package user.v1;
option go_package = "github.com/you/contracts/gen/user/v1;userv1";

service UserService {
  rpc GetUser(GetUserRequest) returns (GetUserResponse);
}

message GetUserRequest {
  string user_id = 1;
}

message GetUserResponse {
  string user_id = 1;
  string email = 2;
  string full_name = 3;
  bool active = 4;
}
```

Ошибки маппятся через `codes.NotFound` → 404 клиенту, `codes.Unavailable` /
`codes.DeadlineExceeded` → 502/503 (сигнал для circuit breaker).

Это единственный контракт, который стоит **физически шарить** как сгенерированный
код между order-service и user-service — оба компилируют один и тот же `.pb.go`.

### 3. Kafka event (`order.events` topic)

Envelope, который целиком лежит в `outbox_events.payload` и публикуется как есть:

```json
{
  "event_id": "uuid",
  "event_type": "order.created",
  "event_version": 1,
  "occurred_at": "2026-08-25T10:00:00Z",
  "payload": {
    "order_id": "uuid",
    "user_id": "uuid",
    "email": "user@example.com",
    "total_amount": "39.98",
    "items": [{ "product_id": "uuid", "quantity": 2 }]
  }
}
```

- `event_id` — ключ идемпотентности на стороне consumer'а (`processed_events`).
- Kafka message key = `order_id` (не `event_id`!) — партиционирование, чтобы события
  одного заказа шли в одну партицию и сохраняли порядок.
- `email` в payload — денормализация: notification-service не зависит от
  user-service синхронно. Trade-off: письмо может уйти на устаревший email,
  если пользователь сменил его между заказом и отправкой уведомления.
- В отличие от gRPC-контракта, JSON-схему события **не обязательно шарить как
  Go-структуру** между сервисами — держи как документ (`contracts/events/*.json`)
  и по копии структуры в каждом сервисе. Это ближе к реальности: рассинхронизация
  схемы между producer и consumer — то, что реально ломается в микросервисах.

## Принятые решения (зафиксировано, чтобы не переобсуждать)

- **ID** — UUID (рассмотреть UUIDv7 для сохранения порядка вставки в индексе;
  `PRIMARY KEY` создаёт индекс автоматически, для FK-колонок — руками).
- **Деньги** — `NUMERIC` в БД, `decimal.Decimal` (shopspring) в Go. Никогда `float64`.
- **order_items.price** — снапшот цены на момент заказа, не ссылка на каталог.
- **gRPC вызывается до открытия транзакции**, а не внутри неё.
- **Outbox worker** использует `SELECT ... FOR UPDATE SKIP LOCKED`, чтобы несколько
  инстансов воркера не публиковали одно и то же событие дважды.
- **Идемпотентность notification-service** — через таблицу `processed_events` и
  `INSERT ... ON CONFLICT DO NOTHING` (Kafka даёт at-least-once, нужно её гасить
  до effectively-once на своей стороне).

## Чек-лист тем для практики (в порядке возрастания сложности)

1. pgxpool + raw-запросы (`QueryRow`, `Query`, `pgx.RowToStructByName`)
2. Транзакции: `BeginTx` / `defer Rollback` / явный `Commit`
3. `pgx.Batch` для вставки `order_items`
4. Outbox: `FOR UPDATE SKIP LOCKED`, поведение при двух параллельных воркерах
5. Kafka producer: партиционирование по ключу, обработка ошибок публикации
6. Kafka consumer group: ручной commit offset после успешной обработки
7. Worker pool в notification-service: bounded channel, backpressure, graceful shutdown
8. gRPC клиент/сервер, маппинг ошибок по `codes.*`
9. Интеграционные тесты с `testcontainers-go` (реальные Postgres + Kafka)
