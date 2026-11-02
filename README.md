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

### Тестирование
- E2E-тесты на отдельном compose-стеке `delivery-e2e`
- Нагрузочное тестирование k6 на изолированном стеке `delivery-load`: профили smoke, stress (поиск предела), regression (гейт регрессий), soak; gRPC-нагрузка через ghz; результаты и предел — в [`load/results/baseline.md`](load/results/baseline.md)
- Go-бенчмарки горячих путей и сравнение с эталоном через benchstat (`task bench`, `task bench-compare`)

### Observability
- OpenTelemetry Collector — приём OTLP и запись в ClickHouse
- ClickHouse — хранилище логов, трейсов и метрик
- Grafana — просмотр трейсов и логов через ClickHouse-датасорс, provisioning