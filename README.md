# 🚚 Delivery Backend

Учебный проект: упрощённый сервис доставки на Go, построенный как набор микросервисов в монорепозитории.

Цель проекта — отработать на практике разработку микросервисов: инструменты, паттерны и подходы, которые встречаются в production-системах.

## Сервисы

- [`gateway`](services/gateway) — публичный HTTP API, единая точка входа для клиентов
- [`order`](services/order) — сервис заказов
- [`user`](services/user) — сервис пользователей
- [`product`](services/product) — сервис товаров
- [`notification`](services/notification) — сервис уведомлений
- [`platform`](platform) — общие платформенные библиотеки для сервисов
- [`proto`](proto) — gRPC-контракты и автогенерированный транспортный код

## Команды

Все команды описаны в `Taskfile.yml`.

Посмотреть список можно через `task --list`.

## Observability

Телеметрия (логи, трейсы, метрики) идёт по OTLP в OTel Collector, хранится в ClickHouse и смотрится в Grafana.
Стек поднимается вместе с остальной инфраструктурой через `task compose-up`.

```
Go-сервисы ──OTLP :4317/:4318──▶ OTel Collector ──native :9000──▶ ClickHouse (БД otel) ◀── Grafana :3001
```

| Сервис | Адрес | Назначение |
|---|---|---|
| Grafana | http://localhost:3001 | UI, вход без логина (anonymous Admin) |
| OTel Collector | `localhost:4317` / `localhost:4318` | приём OTLP gRPC / HTTP |
| OTel Collector | http://localhost:13133 | health check |
| ClickHouse | `localhost:8123` / `localhost:9000` | HTTP / native |

Пользователи ClickHouse (пароли — локальные, заданы в `docker-compose.yml`):

| Пользователь | Пароль | Права |
|---|---|---|
| `default` | `clickhouse` | администратор |
| `otel_writer` | `otel_writer` | Collector: создание таблиц и запись в `otel.*` |
| `grafana_reader` | `grafana_reader` | Grafana: только чтение `otel.*` (`readonly=1`) |

Схему создаёт экспортёр Collector: `otel_logs`, `otel_traces`, `otel_metrics_{gauge,sum,histogram,exponential_histogram,summary}`.
Данные хранятся 72 часа (TTL).

Где смотреть: Grafana → **Explore** → датасорс **ClickHouse** → Query Builder, Query type **Traces** (водопад спанов)
или **Logs**. OTel-режим для таблиц `otel_traces` и `otel_logs` включён в provisioning.
Произвольный SQL по `otel.*` — там же, Editor Type **SQL Editor**.

Если что-то не работает:

- **У `otel-collector` в `docker compose ps` нет статуса `healthy`.** Это нормально: в distroless-образе нечем выполнить
  healthcheck. Проверка — `curl -f localhost:13133`.
- **Датасорс ClickHouse не работает или его нет.** Плагин `grafana-clickhouse-datasource` скачивается при старте Grafana,
  нужна сеть. Ищите ошибку установки в `docker compose logs grafana` и перезапустите `docker compose restart grafana`.
- **В Query Builder выключен «Use OTel».** В provisioning датасорса для `logs` и `traces` нужен и `otelEnabled: true`,
  и `otelVersion` — без версии плагин OTel-режим не включает.

## Что отрабатывается

### Коммуникация между сервисами
- gRPC — синхронное взаимодействие между сервисами (Protobuf, генерация кода через `buf`)
- HTTP / REST — публичный API через gateway-сервис
- Kafka (Redpanda) — асинхронный обмен событиями

### Надёжная доставка событий и консистентность данных
- Transactional Outbox — атомарная запись изменений и событий
- Inbox — идемпотентная обработка входящих сообщений
- CDC (Change Data Capture) — публикация изменений из БД в брокер
- Транзакции и Unit of Work на уровне use case

### Архитектура и проектирование
- Clean Architecture
- Event-Driven Architecture — сервисы взаимодействуют через доменные события
- API Gateway — единая точка входа для клиентов
- Dependency Injection через конструкторы

### Инфраструктура и локальная разработка
- Docker / Docker Compose для локального окружения
- Taskfile для автоматизации задач
- Air для hot reload сервисов при разработке

### Observability
- OpenTelemetry Collector — приём OTLP и запись в ClickHouse
- ClickHouse — хранилище логов, трейсов и метрик
- Grafana — просмотр трейсов и логов через ClickHouse-датасорс, provisioning