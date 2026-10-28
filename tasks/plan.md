# Implementation Plan: test-foundation

> Спек: [SPEC-test-foundation.md](../spec/testing/SPEC-test-foundation.md) · Карта: [SPEC-testing-map.md](../spec/testing/SPEC-testing-map.md) · Задачи: [todo.md](todo.md)

## Overview

Общая тестовая инфраструктура монорепо: модуль `testkit/` (Postgres и Redpanda на testcontainers),
задачи Taskfile `test`/`test-integration`/`mocks`/`cover`, генерация моков mockery v3, линтеры для тестов,
порог покрытия. Работоспособность доказывается пилотом в `order`: unit-тест `CreateOrder` на моках
и integration-тест `OrderRepo.Create`. Боевой код не меняется.

## Architecture Decisions

- **`testkit` — отдельный модуль без зависимости от `platform`.** Иначе возникает цикл модулей: интеграционные тесты `platform` тоже будут импортировать `testkit`. Наружу отдаются только `*pgxpool.Pool`, DSN и адреса брокеров.
- **Изоляция БД — клон шаблона.** На тестовый бинарь один контейнер (`sync.Once`), миграции применяются один раз в `template_app`, каждый `NewDB(t)` делает `CREATE DATABASE ... TEMPLATE`. Это совместимо с `t.Parallel`, snapshot/restore — нет.
- **Миграции применяются библиотекой `golang-migrate`** из того же каталога `migrations/pg`, что и `task migrate-up`. Схема в тестах совпадает с боевой.
- **Моки коммитятся.** `dir: {{.InterfaceDir}}/mocks`, `pkgname: mocks`, `structname: Mock{{.InterfaceName}}`. Свежесть проверяет `task mocks` + `git diff --exit-code`.
- **Покрытие собирается по модулям в `coverage/<module>.out`** (каталог в `.gitignore`), `go-test-coverage` проверяет все профили одним конфигом в корне.
- **Самый рискованный кусок — первым.** Это Docker + шаблонная БД + миграции в Task 1. Если подход не взлетит, остальное не начинаем.

## Dependency Graph

```
Task 1 testkit/pgtest ──┬── Task 2 testkit/kafkatest
                        │
Task 3 Taskfile test/test-integration ──┬── Task 6 pilot integration (нужны 1, 3)
                                        │
Task 4 mockery + моки order ── Task 5 pilot unit (нужны 3, 4)
                                        │
Task 7 линтеры (после 5, 6 — есть что линтить)
Task 8 покрытие (нужны 3, 5, 6)
Task 9 AGENTS.md (в конце, фиксирует итоговые конвенции)
```

## Task List

### Phase 1: testkit
- [ ] Task 1: Модуль `testkit` + `pgtest` со smoke-тестом
- [ ] Task 2: `kafkatest` со smoke-тестом

### Checkpoint: testkit
- [ ] `go test -tags=integration ./...` в `testkit` зелёный, контейнеры удаляются после прогона

### Phase 2: Команды и пилот
- [ ] Task 3: Задачи Taskfile `test` и `test-integration` во всех модулях
- [ ] Task 4: mockery v3: установка, `.mockery.yml`, `task mocks`, моки `order/internal/service`
- [ ] Task 5: Пилотный unit-тест `OrderService.CreateOrder`
- [ ] Task 6: Пилотный integration-тест `OrderRepo.Create`

### Checkpoint: Пилот
- [ ] `task test` (без Docker) и `task test-integration` зелёные из корня
- [ ] Ревью пилотных тестов с пользователем: стиль закрепляется до массового написания

### Phase 3: Контроль качества
- [ ] Task 7: Линтеры `thelper`, `testifylint` (enable-all), `depguard` для `testkit`/`mocks`
- [ ] Task 8: `task cover` + `.testcoverage.yml`
- [ ] Task 9: Раздел «Тесты» в `AGENTS.md`

### Checkpoint: Complete
- [ ] Все Success Criteria спека выполнены
- [ ] `task lint`, `task test`, `task test-integration` зелёные; `task cover` печатает отчёт

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| `CREATE DATABASE ... TEMPLATE` падает, пока к шаблону есть соединения | High | Закрывать migrate и пул до первого клона. Smoke-тест с 10 параллельными `NewDB` в Task 1 |
| Ryuk не стартует в Docker Desktop/colima на macOS | Med | Проверить в Task 1. Fallback — `TESTCONTAINERS_RYUK_DISABLED` + явный `TerminateContainer`, решение с пользователем |
| `testifylint enable-all`/`thelper` массово ругаются на существующие тесты | Med | В Task 7 посчитать нарушения. Если их больше ~20, остановиться и согласовать: чинить сейчас или в модуле `unit` |
| `go-test-coverage` неправильно сопоставляет пути профилей разных модулей | Med | В Task 8 сверить синтаксис конфига по документации, проверить на двух модулях до подключения всех |
| Пороги покрытия красные до модулей `unit`/`integration` | Low | Ожидаемо (спек). `task cover` честно падает, порог не занижается |
| Новые зависимости тянут несовместимые версии `pgx`/`otel` в `go.work` | Low | `task sync` + `task tidy` после каждой задачи с новыми зависимостями |

## Open Questions

- Нет.
