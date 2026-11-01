# Implementation Plan: e2e

> Spec: [spec.md](spec.md) · Tasks: [todo.md](todo.md) · Карта: [map.md](../../../specs/testing/map.md)

## Overview

Модуль `e2e/`: изолированный стек `delivery-e2e` из docker compose (инфраструктура и пять сервисов
из образов) и тесты чёрного ящика через HTTP `gateway`: happy path до строки в БД `notification`,
ошибки API, сквозной трейс в ClickHouse, устойчивость к остановке `notification`/Debezium и к
повторной доставке. Одна команда `task e2e` поднимает стек, прогоняет тесты и убирает стек.

## Граф зависимостей

```
grpcserver: health (T1) ─────────────┐
Dockerfile + .dockerignore (T2) ─────┤
модуль e2e/ + go.work + MODULES (T3) ┤
                                     ▼
              e2e/compose.yaml + task e2e-up/down/logs/images-check (T4)
                                     ▼
              TestMain + хелперы + HappyPath + task e2e/e2e-test (T5)
                     ┌───────────────┼──────────────────┐
                     ▼               ▼                  ▼
           ClientErrors/NotFound   Trace (T7)   хелперы stop/start + NotificationDown/DebeziumDown (T8)
                 (T6)                                   ▼
                                                  Redelivery (T9)
                                     ▼
                       флейк-проверка, итоги в карте (T10)
```

T1–T3 независимы. T6, T7, T8 независимы друг от друга после T5.

## Architecture Decisions

- **Health в `platform/grpcserver`** (решение по спеку, Open Question 4): `New` всегда регистрирует
  `health.NewServer()`; `graceful()` сначала вызывает `Shutdown()` (все сервисы → `NOT_SERVING`),
  затем `GracefulStop`. Отдельной опции нет: health нужен всем gRPC-сервисам. Валидационный
  интерсептор пропускает `HealthCheckRequest` (это уже покрыто тестами `interceptors`).
- **Сборка образа через `go.work`, а не `GOWORK=off`.** `notification` ссылается на `proto`
  псевдоверсией без `replace`: вне workspace сборка пошла бы в сеть за чужой версией. Поэтому в
  контекст копируются `go.work`, `go.work.sum` и все модули из `use` (включая `e2e/`). Порядок
  `COPY`: сначала `go.work*` и все `go.mod`/`go.sum`, `go mod download`, затем исходники — кеш
  слоя зависимостей переживает правки кода.
- **Версии образов:** `golang:1.27`, `gcr.io/distroless/static-debian12:nonroot`,
  `migrate/migrate:v4.19.1` (= `MIGRATE_VERSION` в Taskfile сервисов), `curlimages/curl` —
  фиксированный тег, выбирается в T4 и записывается в спек. Инфраструктура — те же теги, что в
  корневом `compose.yaml`, сверка `task e2e-images-check`.
- **Готовность:** `up --wait` — инфраструктура (healthchecks) и одноразовые контейнеры;
  сервисы — `TestMain` (health / `400` от `gateway`), у distroless нет утилит для healthcheck.
- **Трейс через Link** (Open Question 5): корневой спан `gateway` ищется по
  `has(Links.TraceId, <клиентский id>)`, дальше спаны по его `TraceId`. Схему таблицы создаёт
  экспортёр ClickHouse (`create_schema: true`), колонки проверяются на первом прогоне T7.
- **`task e2e` = up → test → down через `defer`:** стек убирается и при красных тестах;
  `E2E_KEEP=1` отключает `down`.
- **Один пакет `e2e_test`**, тег `e2e` на всех файлах, кроме `env.go`: без тега модуль компилируется
  и `task test`/`lint` по корню его не запускают.

## Task List

### Phase 1: Фундамент
- [x] Task 1: gRPC health в `platform/grpcserver`
- [x] Task 2: Общий Dockerfile сервисов и `.dockerignore`
- [x] Task 3: Каркас модуля `e2e/` в workspace и Taskfile

### Checkpoint: Фундамент
- [ ] `task test`, `task lint` зелёные; пять образов собираются

### Phase 2: Стек
- [x] Task 4: `e2e/compose.yaml` и задачи `e2e-up`/`e2e-down`/`e2e-logs`/`e2e-images-check`

### Checkpoint: Стек
- [ ] Стек поднимается рядом с dev-стеком и убирается без остатка

### Phase 3: Сквозной путь
- [x] Task 5: `TestMain`, хелперы и `TestCreateOrder_HappyPath`; `task e2e`, `task e2e-test`
- [x] Task 6: `TestCreateOrder_ClientErrors` и `TestCreateOrder_NotFound`

### Checkpoint: Сквозной путь
- [ ] `task e2e` зелёный от чистого состояния

### Phase 4: Трейс и устойчивость
- [x] Task 7: `TestTrace_EndToEnd`
- [x] Task 8: `TestResilience_NotificationDown` и `TestResilience_DebeziumDown`
- [x] Task 9: `TestResilience_Redelivery`

### Checkpoint: Готово
- [ ] Task 10: флейк-проверка и итоги модуля в карте
- [ ] Все Success Criteria спека выполнены, ревью человеком

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Debezium-образ без `curl` для healthcheck | Med | Проверить в T4; запасной вариант — `bash -c '</dev/tcp/localhost/8083'` |
| `rpk group seek` отказывает, пока в группе есть участники | Med | T9: после `stop notification` ждать пустую группу (`rpk group describe`, `EventuallyWithT`), затем seek |
| Схема `otel_traces` (`Links.TraceId`) отличается от ожидаемой | Low | T7: сначала `DESCRIBE otel.otel_traces` на поднятом стеке, запрос подгоняется под факт |
| Батчинг коллектора дольше `traceTimeout` (60 с) | Low | Проверить на прогоне; таймаут — константа, правка только с согласия |
| После `start` Debezium коннектор не сразу `RUNNING` | Med | T8: `t.Cleanup` ждёт `RUNNING` через REST, тест ждёт доставку `EventuallyWithT` |
| Сборка пяти образов медленная, `task e2e` долгий | Low | Слой `go mod download` и cache mounts BuildKit; время прогона фиксируется в итогах |
| Порты 28xxx/25xxx заняты на машине | Low | Диапазон выбран вне dev-портов; адреса в `env.go` переопределяются env. Параметризация портов compose — только с согласия |

## Open Questions

- Нет. Решения по health и трейсу внесены в спек (Open Questions 4–5).
