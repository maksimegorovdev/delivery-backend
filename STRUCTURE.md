# Структура монорепы

Этот файл описывает репозиторную/модульную организацию кода. За бизнес-архитектурой,
контрактами и моделями данных — см. [README.md](./README.md).

## Дерево каталогов

```
go-backend-lab/
├── go.work
├── README.md
├── STRUCTURE.md
├── Makefile                        # proto-gen, lint, test, docker-compose таргеты
├── deploy/
│   └── docker-compose.yml          # postgres x3 (по одному на сервис), kafka/redpanda
│
├── contracts/                      # module: github.com/maximegorov/go-backend-lab/contracts
│   ├── go.mod
│   ├── buf.yaml
│   ├── buf.gen.yaml
│   ├── proto/
│   │   └── user/v1/user.proto
│   ├── gen/
│   │   └── user/v1/*.pb.go         # сгенерировано buf, коммитится в репо
│   └── events/
│       └── order.created.v1.json   # JSON Schema события — ДОКУМЕНТ, не Go-структура
│
├── pkg/                             # module: github.com/maximegorov/go-backend-lab/pkg
│   ├── go.mod
│   ├── postgres/                   # pgxpool wiring, health-check
│   ├── kafkax/                     # producer/consumer boilerplate: конфиг, реконнект
│   ├── grpcx/                      # interceptors: logging, recovery, codes.* mapping
│   ├── httpx/                      # middleware: logging, recovery, request-id
│   ├── logger/                     # slog setup
│   └── shutdown/                   # graceful shutdown helper (signal.NotifyContext + errgroup)
│
├── services/
│   ├── order-service/              # module: .../services/order-service
│   │   ├── go.mod
│   │   ├── cmd/order-service/main.go
│   │   ├── internal/
│   │   │   ├── config/
│   │   │   ├── api/                # HTTP-хендлеры (POST /orders)
│   │   │   ├── domain/             # Order, OrderItem — свои, не шарятся
│   │   │   ├── repository/         # pgx-запросы к orders/order_items/outbox_events
│   │   │   ├── outbox/             # poll-loop воркер, FOR UPDATE SKIP LOCKED
│   │   │   ├── kafka/              # producer событий order.events
│   │   │   └── userclient/         # обёртка над gRPC-клиентом к user-service
│   │   ├── migrations/
│   │   └── Dockerfile
│   │
│   ├── user-service/                # module: .../services/user-service
│   │   ├── go.mod
│   │   ├── cmd/user-service/main.go
│   │   ├── internal/
│   │   │   ├── config/
│   │   │   ├── grpcserver/          # реализация UserServiceServer
│   │   │   ├── domain/              # User
│   │   │   └── repository/          # pgx-запросы к users
│   │   ├── migrations/
│   │   └── Dockerfile
│   │
│   └── notification-service/        # module: .../services/notification-service
│       ├── go.mod
│       ├── cmd/notification-service/main.go
│       ├── internal/
│       │   ├── config/
│       │   ├── kafka/               # consumer group, ручной commit offset
│       │   ├── worker/              # worker pool, bounded channel, graceful shutdown
│       │   ├── domain/              # своя копия структуры события (см. README)
│       │   └── repository/          # processed_events dedup
│       ├── migrations/
│       └── Dockerfile
```

## go.work

```
go 1.23

use (
    ./contracts
    ./pkg
    ./services/order-service
    ./services/user-service
    ./services/notification-service
)
```

`go.work` коммитится в репозиторий, чтобы `go build ./...` из корня сразу
работал без дополнительной настройки.

## Ключевые принципы разделения

1. **`contracts`** — единственный модуль, который сервисы импортируют друг у
   друга опосредованно (order-service и user-service оба тянут
   `contracts/gen/user/v1`). Генерируется через `buf generate`, коммитится как
   обычный Go-код (не генерится в CI на лету, чтобы не плодить недетерминизм
   в лабе).

2. **`pkg`** — только инфраструктурный, бизнес-агностичный код (логгер, пул к
   Postgres, обёртки над Kafka/gRPC, graceful shutdown). Никаких `Order`,
   `User`, доменных типов — иначе `pkg` тихо станет общей моделью данных, и
   сервисы перестанут быть независимыми. Это и есть "общие пакеты для
   переиспользования" — но переиспользуется инфраструктура, не домен.

3. **Доменные модели и JSON-схема Kafka-события не шарятся** — каждый сервис
   держит свою копию (order-service — то, что публикует; notification-service
   — то, что парсит). Осознанное решение из README: рассинхронизация схемы
   между producer и consumer — часть того, что реально практикуется в
   микросервисах, и специально не устраняется общей Go-структурой.

4. **`internal/`** в каждом сервисе — Go не даст order-service импортировать
   internal-пакет user-service: граница между сервисами форсируется
   компилятором, а не только соглашением.

5. Каждый сервис — свой `go.mod`, свой `Dockerfile`, свои `migrations/` и
   своя схема БД — соответствует README ("никаких общих таблиц и cross-database
   foreign key").

## Договорённости по инструментам

- **go.work + отдельный go.mod на модуль** (а не единый go.mod на весь
  репозиторий) — сервисы независимо версионируются и деплоятся, ближе к
  реальному продакшен-монорепо; при необходимости сервис можно вынести в
  отдельный репозиторий одной командой.
- **buf** — генерация Go-кода из `user.proto` (`buf generate`), а не голый
  `protoc`.
- **Module path**: `github.com/maximegorov/go-backend-lab`, у каждого модуля
  — свой суффикс (`/contracts`, `/pkg`, `/services/order-service` и т.д.).
