# Implementation Plan: unit

> Спек: [SPEC-unit.md](../spec/testing/SPEC-unit.md) · Карта: [SPEC-testing-map.md](../spec/testing/SPEC-testing-map.md) · Задачи: [todo.md](todo.md)

## Overview

Unit-тесты для всей логики без I/O во всех сервисах и в `platform`, перевод существующих тестов на
testify и английские имена кейсов, fuzz для парсеров внешнего входа. Общий хелпер
`platform/testkit/grpctest` (bufconn) используется в тестах gRPC-транспорта и gRPC-клиентов.
Боевой код меняется только при найденном баге (Prove-It) или ради шва для тестируемости.

## Architecture Decisions

- **`grpctest` — первым.** От него зависят тесты интерцепторов, транспорта `order`/`user`/`product` и клиентов `order`/`gateway`. Это единственная новая конструкция модуля; если bufconn-подход не взлетит с интерцепторами `platform`, узнаем до массовой работы.
- **Нарезка по пакетам, а не по видам тестов.** Задача = пакет (или пара мелких пакетов) целиком: новые тесты + перевод уже существующих тестов этого пакета + моки. Так каждая задача закрывает строку таблицы «Покрытие по пакетам» и не оставляет пакет полупереведённым.
- **Сначала `platform`, потом `order`, потом остальные сервисы.** Пакеты `apperr*` используются в ассертах всех сервисов, интерцепторы — в тестах транспорта. `order` — эталонный сервис: на нём закрепляются образцы тестов клиента и транспорта, остальные сервисы их копируют.
- **Fuzz-инфраструктура вводится вместе с первой целью (`bind`).** Корневой `task fuzz` обходит список `FUZZ_MODULES`, а не все модули: модуль попадает в список в той же задаче, где появляется его первая fuzz-цель. Модули без целей не получают пустую задачу `fuzz`.
- **Порог 80% для пакетов `platform` включается после их тестов** (Task 6), иначе `task cover` краснеет посреди фазы.
- **`.mockery.yml` в сервисе создаётся в задаче, где впервые нужны его моки.** Новые пакеты (`transport/...`) добавляются в конфиг там же.
- **Перевод существующих тестов без собственного пакета в плане** (telemetry, logger, postgres, конфиги сервисов) — отдельными механическими задачами в конце, больше 5 файлов допустимо.

## Task List

### Phase 1: platform — фундамент и чистые функции
- [ ] Task 1: `platform/testkit/grpctest` с собственным тестом
- [ ] Task 2: Тесты `platform/apperr`
- [ ] Task 3: Тесты `apperr/grpcerr`, `apperr/ozzoerr`, `apperr/pgerr`
- [ ] Task 4: Тесты `appctx`, `closer`, `http/response`, `internal/otelenv`
- [ ] Task 5: Тесты и fuzz `http/bind`, задача `task fuzz`
- [ ] Task 6: Порог 80% для пакетов `platform` без I/O

### Checkpoint 1: platform-фундамент
- [ ] `task -d platform test` и `task -d platform lint` зелёные, `task -d platform fuzz` работает
- [ ] Обзор API `grpctest` и образца табличного теста с пользователем

### Phase 2: platform — транспорт и горутины
- [ ] Task 7: Тесты `grpc/interceptors` на `grpctest`
- [ ] Task 8: Тесты `grpcserver` и `grpcclient`, `goleak`
- [ ] Task 9: Тесты `http/middleware`
- [ ] Task 10: Тесты `httpserver` (graceful shutdown), `goleak`
- [ ] Task 11: Unit-тесты `kafkaconsumer`

### Checkpoint 2: platform готов
- [ ] `task -d platform test`, `task -d platform lint` зелёные; `task -d platform cover` — пороги 80% выполнены

### Phase 3: order — эталонный сервис
- [ ] Task 12: Тесты `order/internal/domain`
- [ ] Task 13: Тесты и fuzz `order/internal/event`
- [ ] Task 14: Дополнить тесты `order/internal/service`
- [ ] Task 15: Тесты `order/internal/client` на `grpctest`
- [ ] Task 16: Тесты `order/internal/transport/grpc` на `grpctest`

### Checkpoint 3: образцы закреплены
- [ ] `task -d services/order test`, `lint`, `cover` зелёные
- [ ] Обзор образцов тестов клиента и транспорта с пользователем перед копированием в другие сервисы

### Phase 4: остальные сервисы (задачи независимы, можно параллельно)
- [ ] Task 17: user — моки и тесты `service`
- [ ] Task 18: user — тесты `transport/grpc`
- [ ] Task 19: product — моки, тесты `service` и `transport/grpc`
- [ ] Task 20: notification — моки и тесты `service`
- [ ] Task 21: notification — тесты и fuzz `transport/kafka`
- [ ] Task 22: gateway — моки, тесты `service` и `client`
- [ ] Task 23: gateway — тесты HTTP-транспорта `v1/order`

### Checkpoint 4: все сервисы покрыты
- [ ] `task test`, `task lint`, `task cover` из корня зелёные; `task mocks` без diff

### Phase 5: перевод оставшихся тестов
- [ ] Task 24: Перевод тестов `platform/telemetry`
- [ ] Task 25: Перевод тестов `logger`, `postgres`, `grpc` (otel)
- [ ] Task 26: Перевод тестов конфигов сервисов и `order/repository/pg/outbox_test.go`

### Phase 6: закрытие
- [ ] Task 27: Финальная проверка, покрытие в карте эпика, резюме багов

### Checkpoint: Complete
- [ ] Все Success Criteria спека выполнены
- [ ] Готово к ревью

## Dependency Graph

```
Task 1 grpctest ──┬── Task 7 interceptors ──┬── Task 15 order/client ── Task 22 gateway/client
                  │                         └── Task 16 order/transport ── Task 18 user, Task 19 product
                  └── Task 8 grpcserver/client
Task 2 apperr ── Task 3 apperr/* ── (ассерты ошибок во всех сервисах)
Task 5 bind + task fuzz ── Task 13 order/event fuzz, Task 21 notification fuzz
Task 2..5 ── Task 6 порог platform
Checkpoint 3 ── Phase 4 (задачи 17–23 независимы друг от друга)
Phase 4 ── Phase 5 ── Task 27
```

## Parallelization

- Phase 4: задачи 17–23 трогают разные модули и могут идти параллельно после Checkpoint 3.
- Phase 5 независима от Phase 3–4 (другие файлы), но идёт после них, чтобы не конфликтовать по `config_test.go`.
- Последовательно: Task 1 → 7 → 15/16 (общий хелпер и образец).

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Занятый порт или гонка при старте сервера на loopback | Low | Только `127.0.0.1:0`, адрес не хардкодится; готовность ждём через `require.Eventually`, а не `time.Sleep` |
| Тест выявил баг, исправление меняет контракт (коды ошибок, ответы, формат события) | High | Boundary «Ask first»: красный тест + описание, исправление только после согласия |
| `task cover` требует Docker (тег `integration`) | Low | Пороги проверяются с Docker; для `task test` Docker не нужен — проверяем при остановленном Docker в Task 27 |
| Пакет `service` не дотягивает до 80% без шва (например, время/UUID внутри `domain.NewOrder`) | Med | Проверять формат, а не значение; шов для тестируемости — только после «Ask first» |
| Линтеры (`testpackage`, `paralleltest`, `depguard`) ругаются на новые тесты | Low | `task lint` в verification каждой задачи; white-box только с `//nolint` и обоснованием |
| Flaky под `-race -shuffle=on` в тестах с горутинами | Med | Никаких `time.Sleep`; `require.Eventually`, каналы, `goleak`; прогон `-count=10` в задачах 8, 10, 11 |

## Resolved Questions

1. Старт/остановка `grpcserver.Run` и `httpserver` — loopback `127.0.0.1:0` (как `httptest.NewServer`), внесено в спек.
2. Fuzz `order/internal/event` — заказ собирается из примитивов, число позиций — параметр fuzz.
3. Неизвестный тип события в `notification` → `nil` — корректное поведение (tolerant reader), фиксируется тестом.

## Open Questions

Нет.

## Отложено (вне модуля unit)

- `notification`: битый payload → `InvalidArgument` → `kafkaconsumer` бесконечно откатывает партицию (poison message). Варианты: пропуск с логом, DLQ, лимит повторов в `kafkaconsumer`.
- `notification`: неизвестный или пустой `event_type` пропускается без лога и метрики — добавить след.

В этом модуле тесты фиксируют текущее поведение.
