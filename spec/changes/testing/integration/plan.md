# Implementation Plan: integration

> Спек: [spec.md](spec.md) · Карта: [map.md](../../../specs/testing/map.md) · Задачи: [todo.md](todo.md)

## Overview

Интеграционные тесты для кода с реальными зависимостями (Postgres, Redpanda, сетевые серверы) во всех
сервисах и `platform/kafka/kafkaconsumer`, общий эталон события `order.created` в `contracts/events/`,
шов `App.Addr()` и smoke-тесты `app` всех пяти сервисов. Итог — зелёный `task cover` по всем порогам,
включая `total` ≥ 70%. Боевой код меняется только швом `App.Addr()` и исправлением найденных багов (Prove-It).

## Dependency Graph

```
pgtest.CheckMigrations ─┬─ migrations-тесты всех сервисов (T4, T8, T10, T11)
kafkatest.Produce ──────┼─ kafkaconsumer на Redpanda (T3)
                        └─ Kafka-стек notification (T9) ── smoke notification (T15)
order: repo (T4) → атомарность (T5) → эталон order.created (T6) ─┬─ gRPC-стек order (T7)
                                                                 └─ Kafka-стек notification (T9)
notification repo (T8) ─────────────────────────────────────────── Kafka-стек notification (T9)
шов App.Addr() + smoke order (T12) → user/product (T13), gateway (T14)
всё → task cover и итоги в карте (T16)
```

## Architecture Decisions

- **Хелперы `testkit` — первыми.** `CheckMigrations` и `Produce` — единственные новые API модуля, от них
  зависят задачи всех сервисов. `kafkaconsumer` на Redpanda идёт сразу за ними: это самый рискованный
  асинхронный код (коммит, откат оффсета, ретрай), и проблемы с синхронизацией тестов на брокере лучше
  найти до компонентного теста `notification`.
- **Нарезка по сервисам и слоям одного сервиса.** Задача закрывает строку таблицы «Покрытие по пакетам»
  целиком: репозиторий + миграции, затем компонентный стек. `order` — эталон: на нём закрепляются
  образцы теста репозитория, `CheckMigrations`, компонентного gRPC-стека и smoke `app`; `user`/`product`
  их копируют.
- **Эталон `order.created` создаёт продюсер (T6), консьюмер его только читает (T9).** Файл появляется
  через `-update` по фактическому выводу `OrderEventBuilder` + `OutboxRepo` и проверяется глазами в
  резюме T6; T9 не может начаться раньше.
- **Шов `App.Addr()` вводится на `order`, остальные сервисы повторяют.** `New` открывает листенер,
  закрывает его через `closer` при ошибке; `Run` вызывает `Serve(ctx, lis)`. Порт `127.0.0.1:0` из env.
- **Внешние gRPC-зависимости в smoke `order` и `gateway` — фейковые серверы на loopback-листенере
  в тесте** (`net.Listen("tcp", "127.0.0.1:0")` + `grpc.NewServer`), а не bufconn: `app.New` дозванивается
  по адресу из env.
- **`task cover` закрывается последним.** Пробелы, которые он покажет после всех задач, добирает T16;
  `.testcoverage.yml` не меняется.

## Task List

### Phase 1: testkit и kafkaconsumer
- [x] Task 1: `pgtest.CheckMigrations` с собственным тестом
- [x] Task 2: `kafkatest.Produce` с собственным тестом
- [x] Task 3: Интеграционные тесты `kafkaconsumer` на Redpanda

### Checkpoint 1: инфраструктура
- [ ] `task -d platform test-integration`, `task -d platform lint` зелёные, `goleak` чистый
- [ ] Обзор API `CheckMigrations`/`Produce` и образца асинхронной проверки с пользователем

### Phase 2: order — эталонный сервис и контракт
- [x] Task 4: `order` — репозитории (`OrderRepo`, `OutboxRepo`) и миграции
- [x] Task 5: `order` — атомарность заказа и outbox
- [x] Task 6: Эталон `order.created` и тест продюсера
- [ ] Task 7: `order` — компонентный gRPC-стек

### Checkpoint 2: order
- [ ] `task -d services/order test-integration` и `lint` зелёные
- [ ] Эталон `contracts/events/order.created.json` согласован с пользователем

### Phase 3: notification
- [ ] Task 8: `notification` — репозитории и миграции
- [ ] Task 9: `notification` — компонентный Kafka-стек на эталоне

### Phase 4: user и product
- [ ] Task 10: `user` — репозитории, миграции, компонентный gRPC-стек
- [ ] Task 11: `product` — репозиторий, миграции, компонентный gRPC-стек

### Checkpoint 3: все репозитории и компонентные стеки
- [ ] `task test-integration` зелёный во всех модулях, `task test` работает без Docker
- [ ] Ревью найденных багов с пользователем

### Phase 5: шов App.Addr() и smoke app
- [ ] Task 12: Шов `App.Addr()` и smoke-тест `order`
- [ ] Task 13: Шов и smoke-тесты `user` и `product`
- [ ] Task 14: Шов и smoke-тест `gateway`
- [ ] Task 15: Smoke-тест `notification`
- [ ] Task 16: `task cover` зелёный, итоги в карте эпика

### Checkpoint: Complete
- [ ] Все Success Criteria спека выполнены
- [ ] Резюме модуля (баги, изменения боевого кода) готово, ревью с пользователем

## Parallelization

После Checkpoint 1 независимы: T4–T7 (`order`), T8 (`notification` repo), T10, T11. T9 ждёт T6 и T8.
T13–T15 параллельны после T12. Конфликтов по файлам между сервисами нет.

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Флаки асинхронных тестов на Redpanda (ребаланс группы, задержки коммита) | High | Уникальные топик и группа на тест, только `require.EventuallyWithT`, прогон `-count=5` в верификации T3 и T9 |
| Тест ретрая `kafkaconsumer` медленный из-за `retryBackoff` 1s | Low | `kafkaconsumer.WithRetryBackoff` с малым значением в тесте |
| `Serve` сам закрывает листенер, а `closer` из `New` закроет его повторно → ошибка в `Close` | Med | Листенер в `closer` только на время `New` (при ошибке), либо игнор `net.ErrClosed`; проверяется smoke-тестом «`Close` без ошибок» в T12 |
| `pgtest`: один каталог миграций на бинарь, а `CheckMigrations` нужен в том же пакете, что и `NewDB` | Med | `CheckMigrations` использует тот же каталог и общий контейнер, БД без `TEMPLATE` (по спеку) |
| Smoke `app` трогает глобалы (`slog.SetDefault`, otel) и env | Med | Тесты без `t.Parallel`, телеметрия выключена через env, `Close` в `t.Cleanup` |
| Smoke `order`/`gateway` нужны внешние gRPC-сервисы | Med | Фейковые серверы на loopback в тесте (см. решения) |
| Тест выявит баг, меняющий контракт (коды ошибок, формат события) | Med | Boundaries спека: стоп и вопрос пользователю до исправления |
| `total` ≥ 70% не наберётся после всех задач | Low | T16 добирает по отчёту `task cover`; пороги не меняются |

## Open Questions

Нет: оба вопроса спека решены (эталон в `contracts/events/`, шов `App.Addr()`).
