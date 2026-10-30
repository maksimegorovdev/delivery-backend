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
| `test-foundation` | [SPEC-test-foundation.md](SPEC-test-foundation.md) | approved |
| `unit` | [SPEC-unit.md](SPEC-unit.md) | done |
| `integration` | — | todo |
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
- Контракт событий Kafka — golden-тесты в модуле `integration`: один эталон в `testdata/` используется и продюсером (`order`), и консьюмером (`notification`).
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
`grpcserver`/`httpserver` (`Run` — обёртка над ним, чтобы тесты слушали свой листенер);
комментарий и `coverage-ignore` в `platform/internal/otelenv` (недостижимая ветка `panic`);
конфигурация Taskfile/go.mod.
