# Capability Map: Testing

Тестовая инфраструктура и покрытие всех сервисов и `platform` по уровням пирамиды:
unit → integration → e2e → load. Цель — уровень production-систем: каждый уровень запускается
одной командой Taskfile, покрытие контролируется порогом, предел производительности системы измерен.

| Module id | Ответственность | Зависит от |
|---|---|---|
| `test-foundation` | Зависимости (`testify`, `mockery` v3, `goleak`), конвенции (имена, `t.Parallel`, `-shuffle`, build-тег `integration`), конфиги `mockery` и `go-test-coverage`, линтеры для тестов, Taskfile-задачи `test`, `test-integration`, `cover`, `mocks`. Модуль `testkit/` в `go.work`: Postgres и Redpanda на `testcontainers-go`. Пилот: по одному unit- и integration-тесту в `order` | — |
| `unit` | Перевод существующих тестов на testify. Unit-тесты без I/O: `domain`, `service` (моки mockery), транспорт (`httptest` для gateway, `bufconn` для gRPC), mapper/dto, пакеты `platform`. `goleak` в пакетах с горутинами, fuzz для парсеров входных данных | `test-foundation` |
| `integration` | Тесты с реальными зависимостями через `testkit`: репозитории `pg`, миграции up/down, `TxManager`/Unit of Work, outbox/inbox и идемпотентность, `kafkaconsumer` на Redpanda, gRPC client ↔ server. Контракт событий Kafka: golden-тест в `order` (событие в outbox совпадает с эталоном в `testdata/`) и парный тест в `notification` (эталон разбирается корректно) | `test-foundation` |
| `e2e` | Чёрный ящик против стека из docker compose: HTTP gateway → order → outbox → Debezium → Kafka → notification. Отдельный модуль `e2e/`, тег `e2e`, асинхронные проверки через `require.EventuallyWithT` | `integration` |
| `load` | Go-бенчмарки горячих путей (+ `benchstat`), нагрузка на HTTP gateway (`k6`) и gRPC сервисов (`ghz`). Поиск предела: ступенчатый рост RPS до деградации (p95, ошибки), фиксация baseline и узкого места | `e2e` |

Build order: `test-foundation` → `unit`, `integration` (параллельно) → `e2e` → `load`

## Спеки модулей

| Module id | Спек | Статус |
|---|---|---|
| `test-foundation` | [spec](test-foundation/spec.md) | done |
| `unit` | [spec](unit/spec.md) | done |
| `integration` | [spec](integration/spec.md) | done |
| `e2e` | — | todo |
| `load` | — | todo |

## Принятые решения

- CI пока не делаем: все уровни запускаются локально через Taskfile. Задачи пишутся так, чтобы CI позже просто их вызывал.
- Порог покрытия: ≥ 80% для `domain` и `service`, ≥ 70% в целом. Из подсчёта исключены сгенерированный код (`proto/gen`, моки, `docs`) и `cmd`.
- Ассерты: `testify/require` по умолчанию, `assert` — для нескольких независимых проверок подряд. `testify/suite` не используем: плохо сочетается с `t.Parallel`, табличные тесты проще.
- Моки: `mockery` v3 с шаблоном `testify` (типизированный `EXPECT()`). Ручной `testify/mock` не пишем: строковые имена методов не ловятся компилятором. `gomock` не берём: проект уже на testify. Для тривиальных зависимостей (`TxManager`) — ручные фейки.
- Существующие тесты переводятся на testify (модуль `unit`), имена кейсов — на английский.
- Имена тестовых кейсов (`name`) на английском, комментарии на русском.
- e2e на docker compose, а не на testcontainers: стек с Debezium тяжёлый и уже описан в `docker-compose.yml`.
- Контракты gRPC проверяет уже настроенный `buf breaking` (`task -d proto breaking`, правило `FILE`); отдельного модуля под контракты нет.
- Контракт событий Kafka — golden-тесты в модуле `integration`: один эталон в `contracts/events/` в корне репозитория используется и продюсером (`order`), и консьюмером (`notification`).
- Smoke-тест `app` получает фактический адрес через шов `App.Addr()`, а не поиском свободного порта.
- Нагрузка: целевых SLO нет, задача — найти предел и узкое место, зафиксировать baseline.

## Итоги модуля `unit`

Покрытие снято `task cover` (statements, без `mocks`, `cmd`, `docs`). Пакеты без тестов:
`domain` в `user`/`product`/`notification` (только типы), `app`, `repository/pg` — их покрывает модуль `integration`.

| Пакет | Покрытие |
|---|---|
| `platform` (все пакеты), итого | 92.6% (каждый пакет ≥ 80%) |
| `platform/apperr`, `apperr/*`, `appctx`, `closer`, `grpc/grpcclient`, `http/bind`, `http/middleware`, `http/response` | 100% |
| `platform/grpc/interceptors`, `grpc/grpcserver` | 97.4%, 91.7% |
| `platform/http/httpserver`, `postgres`, `telemetry`, `logger` | 92.3%, 92.5%, 91.3%, 90.7% |
| `platform/kafka/kafkaconsumer`, `testkit/grpctest` | 85.9%, 85.0% |
| `order/internal/domain`, `event`, `service`, `client`, `transport/grpc` | 100% |
| `user`, `product`: `service`, `transport/grpc` | 100% |
| `notification`: `service`, `transport/kafka`, `transport/kafka/order` | 100% |
| `gateway`: `service`, `client`, `transport/http/v1/order` | 100% |

`total` по сервисам (порог 70% — общая цель с модулем `integration`): gateway 58.4%, order 69.0%,
user 34.5%, product 32.0%, notification 44.8%. Остаток — `app` и `repository/pg`. `task cover` красный по `total` до модуля `integration` — принято осознанно, гейт `total` для `unit` не действует.

Баги: в `notification` `order.created` с не-UUID `event_id`/`order_id` проходил разбор и падал
только на INSERT в inbox. Теперь идентификаторы проверяются и приводятся к канонической форме
(`uuid.Parse` принимает и `urn:uuid:`, который Postgres отвергает). Невалидное сообщение
по-прежнему откатывает оффсет и блокирует партицию: `kafkaconsumer` ретраит любую ошибку,
отсев poison-сообщений (skip/DLQ) — отдельная задача.

Изменения вне тестов: проверка UUID в `notification`; `Serve(ctx, lis)` в
`grpcserver`/`httpserver` — единственная точка запуска, листенер открывает `platform/listener.Listen`;
комментарий и `coverage-ignore` в `platform/internal/otelenv` (недостижимая ветка `panic`);
конфигурация Taskfile/go.mod.

## Итоги модуля `integration`

Покрытие снято `task cover` (statements, без `mocks`, `cmd`, `docs`); `task cover` зелёный во всех модулях.

| Сервис | `repository/pg` | `app` | `total` |
|---|---|---|---|
| gateway | — (нет БД) | 91.8% | 91.2% |
| order | 100% | 88.9% | 96.5% |
| user | 88.9% | 90.4% | 93.0% |
| product | 92.9% | 90.0% | 93.2% |
| notification | 93.8% | 81.0% | 91.4% |
| platform | — | — | 92.2% (каждый пакет ≥ 80%) |

`domain`/`service` во всех сервисах ≥ 80% (`service` — 100%; `domain` в `user`/`product`/`notification`/`gateway` — только типы, без тестов).

Что сделано: `pgtest.CheckMigrations` (up → down → up, ловит неполный `down`) подключён во всех четырёх сервисах с миграциями; `kafkatest.Produce`; тесты `kafkaconsumer` на Redpanda (коммит, откат с ретраем, остановка по ctx, `goleak`); атомарность `order` (ошибка `OutboxRepo.Save` откатывает `orders`, `order_items`, `outbox_events`); единый эталон `contracts/events/order.created.json` (продюсер `order` сверяет, `notification` использует); компонентные gRPC-стеки `order`/`user`/`product`; компонентный Kafka-стек `notification` (обработка, идемпотентность, неизвестный тип не блокирует партицию); smoke-тесты `app` во всех пяти сервисах.

Баги: интеграционные тесты новых багов не выявили. Зафиксировано текущее поведение: невалидное сообщение в `notification` ретраится и блокирует партицию (`kafkaconsumer` откатывает оффсет при любой ошибке; skip/DLQ — отдельная задача).

Изменения боевого кода: шов `App.Addr()` в `gateway`/`order`/`user`/`product` (листенер открывается в `New`, `Run` вызывает `Serve(ctx, lis)`, повторное закрытие листенера не ошибка); в `notification` — правка замечаний линтера в `orderCreatedIDs` (именованные результаты убраны, ссылка `[uuid.Parse]`), поведение не менялось.
