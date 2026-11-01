# Implementation Plan: load

> Spec: [spec.md](spec.md) · Tasks: [todo.md](todo.md) · Map: [testing](../../../specs/testing/map.md)

## Overview

Нагрузочный модуль: изолированный стек `delivery-load` с лимитами ресурсов, сценарии k6 (smoke,
stress, regression, soak) по `POST /api/v1/orders`, ghz по четырём gRPC-методам, измерение лага
асинхронной цепочки, дашборд `Load` в Grafana, Go-бенчмарки горячих путей с `benchstat` и
capacity-отчёт `load/results/baseline.md`. Боевой код сервисов не меняется — только новые `_test.go`.

## Граф зависимостей

```
load/compose.yaml + load/Taskfile.yml (load-up/down)          ── T1
    │
    ├── seed (load/sql/seed.sql + получение id) → k6 smoke     ── T2
    │       │
    │       ├── k6 → OTLP → ClickHouse → дашборд Load          ── T3
    │       │
    │       ├── stress + разметка ступеней + docker stats       ── T4
    │       │       │
    │       │       ├── лаг цепочки (lag.sql, rpk)              ── T5
    │       │       │
    │       │       └── полный прогон → baseline.md (HTTP)      ── T7 ← T3, T5, T6
    │       │               │
    │       │               ├── regression (пороги из baseline) ── T8
    │       │               └── soak (30 мин, снимки SQL)       ── T9
    │       │
    │       └── ghz по 4 методам                                ── T6
    │
бенчмарки (независимы от стека)
    tooling + gateway ── T10 → platform ── T11 → order, notification + baseline.txt ── T12
```

Ветка бенчмарков (T10–T12) не зависит от стека и может идти параллельно с T1–T9.

## Architecture Decisions

- **Стек — копия `e2e/compose.yaml`, а не `extends`/override.** Load-стеку нужны другие порты (`38xxx`),
  `LOG_LEVEL=info`, лимиты ресурсов, Grafana и профиль `tools`; overlay поверх e2e связал бы два
  модуля и сделал бы правку e2e рискованной для нагрузки. Дрейф версий ловит проверка образов.
- **Проверка образов обобщается:** `e2e-images-check` превращается во внутреннюю задачу
  `images-check FILE=... PREFIX=...`, её вызывают `e2e-up` и `load-up`. Исключения те же
  (образы сервисов, `migrate`, `curl`) плюс `grafana/k6` и `ghz` — их в корневом compose нет.
- **Seed — отдельный SQL в load-стеке, не миграции сервисов.** В миграциях `product` два товара,
  а сценарию нужно 1–3 товара в заказе и `GetProducts` по 10 id. Одноразовый контейнер
  `seed-product` (образ `postgres:18`, `psql -f load/sql/seed.sql`) после `migrate-product`
  добавляет товары до ≥ 10. Код и конфигурация сервисов не меняются.
- **Seed-id передаются через env, а не из `setup()` по сети.** k6 не умеет в SQL; `load/Taskfile.yml`
  получает id через `docker compose exec -T <pg> psql -At` и передаёт `SEED_ADDRESS_ID`,
  `SEED_PRODUCT_IDS` (через запятую) в `docker compose run k6`. `setup()`/`loadSeed()` разбирает env
  и падает при пустом значении. Те же переменные подставляются в `--data` ghz.
- **Лаг — одним SQL через `dblink` из `notification-postgres` в `order-postgres`.** `dblink` есть в
  `contrib` образа `postgres:18`; `CREATE EXTENSION IF NOT EXISTS dblink` выполняет сам `lag.sql`,
  только в load-стеке. Соединение: `inbox_events.aggregate_id` = `orders.id`, лаг =
  `notifications.created_at − orders.created_at` (через `event_id`). Ступень определяется по
  времени заказа и границам ступеней из summary.
- **Границы ступеней** считаются из конфигурации профиля (старт прогона + N × длительность ступени),
  `handleSummary` пишет их в summary JSON рядом с метриками; тег `stage` выставляется в сценарии по
  прошедшему времени (`exec.scenario.startTime`). Это же — точки привязки для будущего профилирования.
- **`docker stats` и `rpk group describe`** снимает фоновый сэмплер в `load/Taskfile.yml` на время
  `load-stress`/`load-soak` (раз в 5 с / в конце ступени) → `load/results/raw/<run>/`.
- **Метрики k6 → OTLP** в `otel-collector` load-стека (встроенный вывод `opentelemetry` k6, gRPC
  `otel-collector:4317`). Имя вывода и env проверяются по документации k6 фиксированной версии в T3.
- **`benchstat` — `tool` в `e2e/go.mod`.** Модуль тестовый и никем не импортируется, поэтому tool-
  зависимость не попадает в граф модулей сервисов и `platform`. `task bench` проходит по модулям
  с бенчмарками и склеивает вывод в `load/bench/new.txt` (`benchstat` различает пакеты по `pkg:`).
- **Регрессионный гейт — `thresholds` k6** с числами из baseline в `k6/lib/config.js`; для ручной
  проверки «падает на заниженном пороге» порог переопределяется env (`REGRESSION_P95_MS`).

## Task List

### Phase 1: Стек и первый сценарий (высокий риск — раньше)
- [ ] Task 1: Load-стек и `load-up`/`load-down`
- [ ] Task 2: Seed и `load-smoke`, `task load`
- [ ] Task 3: Метрики k6 в ClickHouse и дашборд `Load`

### Checkpoint 1
- [ ] `task load` проходит от чистого состояния, стек убирается и при падении
- [ ] Метрики k6 и сервисов на дашборде `Load` во время smoke
- [ ] Ревью с пользователем

### Phase 2: Инструменты поиска предела
- [ ] Task 4: Профиль `stress`, разметка ступеней, сэмплер `docker stats`, разведочный прогон
- [ ] Task 5: Лаг асинхронной цепочки (`load-lag`, `rpk`)
- [ ] Task 6: gRPC-нагрузка ghz (`load-grpc`)

### Checkpoint 2
- [ ] Ступени `stress` выбраны по разведке (8–15 до предела) и записаны в `config.js`
- [ ] `load-lag` и `load-grpc` дают таблицы по ступеням
- [ ] Ревью с пользователем

### Phase 3: Baseline, regression, soak
- [ ] Task 7: Полный прогон, узкое место, `baseline.md`
- [ ] Task 8: Профиль `regression` и `load-regression`
- [ ] Task 9: Профиль `soak` и `load-soak`

### Checkpoint 3
- [ ] `baseline.md` закрывает разделы 1–7 спеки
- [ ] `load-regression` зелёный, падает на заниженном пороге
- [ ] Ревью с пользователем (ask first: перезапись baseline, найденные проблемы)

### Phase 4: Go-бенчмарки (параллельно с фазами 1–3)
- [ ] Task 10: Инструменты `bench`/`bench-compare`/`bench-baseline` и бенчмарки gateway
- [ ] Task 11: Бенчмарки `platform`
- [ ] Task 12: Бенчмарки `order`, `notification` и `baseline.txt`

### Checkpoint: Complete
- [ ] Все Success Criteria спеки отмечены
- [ ] `task lint`, `task test` зелёные; `task test` не медленнее, чем до модуля
- [ ] Итоги модуля в `map.md`, спека опубликована, change в архиве (по `spec-workflow`, после подтверждения)

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Генератор (k6) на том же хосте упирается раньше системы | High | Лимиты стека ≤ 8 CPU, k6 ≥ 3 CPU; `dropped_iterations` и CPU контейнера k6 в отчёте; если k6 у лимита — это предел генератора, а не системы, так и фиксируется |
| Ноутбук троттлит / фоновые процессы искажают числа | Med | Окружение и время прогона в отчёте; разведка и полный прогон подряд; повтор подозрительной ступени |
| Предел упирается в фейкового единственного пользователя/адрес | Low | Принято спекой, отмечается в отчёте |
| Встроенный OTLP-вывод k6 нестабилен/меняет имя между версиями | Med | Фиксированная версия образа, проверка по документации в T3; если вывод не работает — отчёт строится по summary JSON, а k6-панели дашборда переносятся отдельной задачей (ask first) |
| `dblink` недоступен в образе | Low | Проверка в T5 первым шагом; запасной путь — `COPY ... TO STDOUT` из обеих БД и соединение `join`/`awk` в Taskfile |
| ClickHouse/Collector сами становятся узким местом под нагрузкой телеметрии | Med | Их CPU — в `docker stats`; при подозрении — контрольная ступень с `OTEL_ENABLED=false` (ask first, это изменение условий) |
| Soak 30 мин и stress долгие — прогоны срываются | Low | `LOAD_KEEP=1`, сырые результаты пишутся по ходу, ступень можно перезапустить отдельно |
| Сырые результаты раздувают репозиторий | Med | `raw/` в `.gitignore`, кроме файлов, на которые ссылается `baseline.md`; summary без `--out json` построчных точек |

## Open Questions

- Спека в примере проверяет `data.order_id`, а gateway отдаёт `data.id` (`ResponseDTO.ID`).
  План проверяет `data.id`. Поправить пример в спеке (минорная правка без смены поведения) —
  нужно ли переводить спеку в `draft` и переутверждать, или достаточно правки с вашего «ок»?
