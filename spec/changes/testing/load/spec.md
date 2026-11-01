# Spec: load

> Status: **approved** · Depends on: [e2e](../../../specs/testing/e2e/spec.md)

## Objective

Измерить, сколько выдерживает система, и найти, что её ограничивает. Целевых SLO нет (решение карты),
поэтому задача не «уложиться в число», а:

1. **Найти предел** — максимальный устойчивый RPS на `POST /api/v1/orders` до деградации.
2. **Найти узкое место** — какой компонент упирается первым (сервис, пул соединений Postgres,
   Debezium, консьюмер `notification`) и почему, с подтверждением метриками, трейсами и `docker stats`.
3. **Зафиксировать baseline** так, чтобы следующую регрессию можно было поймать одной командой.

Пользователь — разработчик проекта (и AI-агент). Результат:

- `task load-smoke` за ~1 минуту проверяет, что сценарии и стек работают;
- `task load-stress` ступенчато поднимает RPS до деградации и пишет отчёт со ступенями;
- `task load-regression` гоняет фиксированную нагрузку и **падает**, если p95 или доля ошибок хуже
  порогов, выведенных из baseline (так в компаниях ловят регрессии производительности);
- `task bench` / `task bench-compare` — Go-бенчмарки горячих путей и сравнение с эталоном через `benchstat`;
- во время прогона метрики k6 видны в Grafana рядом с метриками сервисов (RED, runtime Go, пул pgx);
- в репозитории лежит capacity-отчёт: железо, версии, ступени, предел, узкое место, рекомендации.

### Как это принято делать (и что из этого берём)

| Практика | Берём | Как |
|---|---|---|
| Типы тестов: smoke, load, stress/breakpoint, spike, soak | smoke, stress, load (он же regression), soak | Один набор сценариев k6, тип выбирается env `PROFILE` |
| Открытая модель нагрузки (arrival rate), а не «N виртуальных пользователей» | да | `ramping-arrival-rate` / `constant-arrival-rate`: RPS задаётся, а не получается. Иначе медленная система сама себе снижает нагрузку (coordinated omission) |
| Критерий деградации записан заранее | да | см. «Критерий предела» |
| Метрики генератора — в общую observability | да | k6 → OTLP → OTel Collector → ClickHouse → Grafana-дашборд «Load» |
| Пороги как регрессионный гейт | да | `thresholds` k6 в профиле `regression`, ненулевой код выхода |
| Профилирование под нагрузкой | отдельной спекой | см. «Связь с профилированием» |
| Эталоны бенчмарков в репозитории + `benchstat` | да | `load/bench/baseline.txt` |
| Письменный capacity-отчёт | да | `load/results/baseline.md`, сырые summary JSON рядом |
| Отдельное изолированное окружение, близкое к prod по ресурсам | да, в пределах ноутбука | Свой compose-стек с лимитами CPU/памяти |

Вне скоупа: CI (решение карты), распределённая генерация нагрузки, автоскейлинг/несколько реплик
сервисов, оптимизация найденного узкого места (только фиксация и рекомендации; правки — отдельная
change), нагрузка на Debezium/Redpanda в отрыве от системы, spike-тест, профилирование (pprof,
continuous profiling) — отдельная спека, см. «Связь с профилированием».

## Tech Stack

| Что | Чем |
|---|---|
| Стек | Docker Compose, `load/compose.yaml`, проект `delivery-load`. За основу — `e2e/compose.yaml` |
| Ресурсы | `deploy.resources.limits` (`cpus`, `memory`) на каждый контейнер. Сумма лимитов стека ≤ 8 CPU, генератору остаётся ≥ 3 CPU (хост 12 CPU / 16 ГБ) |
| HTTP-нагрузка | `grafana/k6` (Docker-образ, фиксированная версия), сценарии на JavaScript |
| gRPC-нагрузка | `ghz` (Docker-образ, фиксированная версия), рефлексия не нужна: `.proto` монтируются из `proto/proto` |
| Метрики k6 | встроенный OTLP-вывод k6 → существующий `otel-collector` load-стека → ClickHouse |
| Дашборд | Grafana в load-стеке, provisioning из `deploy/grafana`, новый дашборд `Load` |
| Бенчмарки | `testing.B` (`b.Loop()`), `golang.org/x/perf/cmd/benchstat` через `go tool` |
| Лаг цепочки | SQL на чтение: `orders.created_at` ↔ `notifications.created_at` (+ метрики консьюмера, если есть в каталоге) |

Новые Docker-образы: `grafana/k6`, `ghz`, `grafana/grafana` (уже в корневом compose). Новая
Go-зависимость: `golang.org/x/perf` как `tool` в `go.mod` модуля, где живёт `task bench`.
Точные версии — в плане.

## Commands

```
task load-up            # docker compose -f load/compose.yaml -p delivery-load up -d --build --wait + ожидание готовности
task load-down          # docker compose ... down -v
task load-smoke         # PROFILE=smoke: 5 RPS, 1 мин, проверка 201 и форм ответа; падает при любой ошибке
task load-stress        # PROFILE=stress: ступени RPS до деградации, summary → load/results/raw/
task load-regression    # PROFILE=regression: постоянный RPS = 70% от предела, 5 мин, thresholds из baseline
task load-soak          # PROFILE=soak: тот же RPS, 30 мин — утечки, рост таблиц и лага, дрейф p99
task load-grpc          # ghz по каждому gRPC-методу, ступени concurrency, JSON → load/results/raw/
task load-lag           # отчёт по лагу асинхронной цепочки за последний прогон (SQL)
task load               # load-up → load-smoke → load-stress → load-down (down и при падении)
task bench              # go test -run=^$ -bench=. -benchmem -count=10 по пакетам с бенчмарками → load/bench/new.txt
task bench-compare      # benchstat load/bench/baseline.txt load/bench/new.txt
task bench-baseline     # перезаписать baseline.txt текущим new.txt (вручную, после осознанного изменения)
```

`LOAD_KEEP=1` не убирает стек после прогона.

## Project Structure

```
load/
  Taskfile.yml                 → задачи модуля (корневой Taskfile делегирует)
  compose.yaml                 → стек delivery-load (см. «Стек»)
  k6/
    lib/config.js              → профили smoke/stress/regression/soak, пороги
    lib/seed.js                → получение seed-id (setup())
    create_order.js            → сценарий POST /api/v1/orders
  ghz/
    <service>_<method>.json    → конфиги ghz: метод, payload, ступени
  sql/lag.sql                  → лаг outbox → notification
  bench/
    baseline.txt               → эталон бенчмарков (коммитится)
    new.txt                    → последний прогон (в .gitignore)
  results/
    baseline.md                → capacity-отчёт (коммитится)
    raw/                       → summary JSON k6/ghz (коммитится только то, на что ссылается baseline.md)
deploy/grafana/dashboards/load.json   → дашборд Load
services/**/*_test.go          → бенчмарки рядом с кодом, в тех же пакетах
```

`load/` — не Go-модуль (Go-кода в нём нет), в `go.work` не добавляется.

### Стек (`load/compose.yaml`)

- Тот же состав, что в e2e (4 Postgres, Redpanda, Debezium, ClickHouse, OTel Collector, миграции,
  регистрация Debezium, 5 сервисов) **плюс Grafana**. Версии образов совпадают с корневым
  `compose.yaml` — проверка как `task e2e-images-check` (переиспользовать или обобщить).
- Свой диапазон портов (`38xxx`), чтобы не конфликтовать с dev и e2e.
- Лимиты ресурсов на каждый контейнер, значения записываются в отчёт: без них результат не
  воспроизводим и не сравним.
- `OTEL_ENABLED=true`, `LOG_LEVEL=info` (debug-логи искажают результат).
- k6 и ghz запускаются `docker compose run --rm` в сети стека (профиль compose `tools`), ходят по
  именам сервисов, а не через проброшенные порты хоста.
- Postgres без томов: каждый `load-up` стартует с чистой БД, результаты сравнимы между прогонами.

### Данные

Gateway использует одного фейкового пользователя (`FAKE_AUTH_USER_ID`), поэтому все заказы — от
одного пользователя и одного адреса. Это принимается как есть и отмечается в отчёте (горячая строка
адреса/пользователя на чтение — не проблема для Postgres). Товары — seed из миграций `product`,
сценарий случайно выбирает 1–3 товара и количество 1–5, чтобы запросы не были одинаковыми.
Seed-id получаются в `setup()` k6 (способ — в плане: SQL через одноразовый контейнер или env).

## Сценарии

### HTTP (k6, `create_order.js`)

| Профиль | Executor | Нагрузка | Назначение |
|---|---|---|---|
| `smoke` | `constant-arrival-rate` | 5 RPS × 1 мин | Сценарий и стек работают. `checks` = 100%, иначе код ≠ 0 |
| `stress` | `ramping-arrival-rate` | от 50 RPS, ступень +50 RPS каждые 2 мин (30 с разгон + 90 с плато), до 2000 RPS или срабатывания `abortOnFail` | Найти предел |
| `regression` | `constant-arrival-rate` | 70% от найденного предела, 5 мин | Гейт регрессий, пороги из baseline |
| `soak` | `constant-arrival-rate` | 70% предела, 30 мин | Утечки, рост лага, деградация во времени (см. «Soak») |

### Soak

Сравниваются первые и последние 5 минут прогона (и тренд за весь прогон на дашборде `Load`):

| Что | Признак проблемы | Источник |
|---|---|---|
| Память, горутины сервисов | монотонный рост без плато | `go.memory.used`, `go.goroutine.count` |
| Соединения пула pgx | `total_connections` растёт или упирается в `max_connections` | `pgxpool.*` |
| p99 `create_order` | последние 5 мин > 1.5× первых 5 мин | k6 |
| Лаг цепочки | p95 лага растёт к концу | `sql/lag.sql`, `rpk group describe` |
| Таблицы `outbox_events`, `inbox_events`, `notifications` | размер и время запросов к ним | `pg_total_relation_size` в начале и в конце |
| WAL / слот репликации Debezium | `pg_replication_slots` `confirmed_flush_lsn` отстаёт, WAL растёт | SQL к `order`-Postgres |

Найденная проблема фиксируется в отчёте с доказательством; исправление — отдельная change (ask first).

Числа ступеней `stress` выше — исходные. Первым шагом плана делается разведочный прогон (грубые
ступени, например ×2: 25 → 50 → 100 → … до деградации), по нему выбираются старт, шаг и верхняя
граница `stress` так, чтобы до предела было 8–15 ступеней. Выбранные числа записываются в
`k6/lib/config.js` и в отчёт.

Проверки в каждой итерации: статус `201`, в ответе есть `order_id`. Метрики k6 размечены тегом
`stage` (номер ступени), чтобы в Grafana и в summary считать p50/p95/p99 и ошибки по ступеням.

### Критерий предела

Ступень считается **деградировавшей**, если на её плато выполнено хотя бы одно:

- доля ошибок (`http_req_failed`) > 1%;
- p95 `http_req_duration` > 3× p95 первой ступени;
- достигнутый RPS < 95% целевого или k6 сообщает `dropped_iterations` (генератор/система не успевает);
- лаг асинхронной цепочки растёт всё плато (консьюмер не догоняет) — фиксируется отдельно как
  «предел асинхронной части», синхронный предел от этого не меняется.

**Предел** = последняя ступень без деградации. Числа (1%, 3×, 95%) приняты; менять — через этот спек
(ask first).

### gRPC (ghz)

По каждому методу отдельно, в обход gateway, чтобы понять, чей предел ниже:

| Метод | Payload |
|---|---|
| `order.v1.OrderService/CreateOrder` | seed-пользователь, seed-товары |
| `product.v1.ProductService/GetProducts` | 1 и 10 id (два прогона) |
| `user.v1.UserService/GetUser` | seed-пользователь |
| `address.v1.AddressService/GetAddress` | seed-адрес |

Ступени по `--concurrency` (например 10, 50, 100, 200) при `--rps` без ограничения, длительность
ступени 60 с. Результат — JSON ghz по ступеням: RPS, p50/p95/p99, ошибки по кодам.

### Лаг асинхронной цепочки

Для каждого заказа прогона: `notifications.created_at − orders.created_at` (две БД — `sql/lag.sql`
запускается по обеим и соединяется по `order_id` в отчёте; способ — в плане). Отчёт: p50/p95/max
по ступеням и вывод, догоняет ли консьюмер. Дополнительно — лаг группы `notification` через
`rpk group describe` в конце каждой ступени.

### Поиск узкого места

На ступени перед пределом и на первой деградировавшей:

1. Дашборд `Load` + существующие RED/runtime-метрики: у какого сервиса растёт latency и CPU,
   упирается ли контейнер в лимит CPU/памяти (`docker stats` в отчёт).
2. Пул pgx: метрики `pgxpool.*` уже собираются (`otelpgx.RecordStats`, см.
   [каталог метрик](../../../specs/observability/metrics/metrics-catalog.md)). На дашборде `Load` —
   панели по каждому сервису с БД: `acquired_connections` / `max_connections` (насыщение пула),
   скорость `empty_acquire` и `empty_acquire_wait_time` (ожидание свободного соединения),
   `canceled_acquires`, `acquire_duration`.
3. Трейсы из ClickHouse: какой спан занимает большую часть времени на медленных запросах (p99).
Вывод в отчёте: компонент, доказательства (график/запрос), рекомендации. Если без профиля нельзя
сказать, *что именно* внутри сервиса тратит CPU/память, отчёт так и говорит: компонент найден,
причина внутри — после модуля профилирования.

### Go-бенчмарки

Горячие пути, которые выполняются на каждый запрос, без I/O:

| Пакет | Что |
|---|---|
| `gateway/.../transport/http/v1/order` | разбор и валидация запроса создания заказа, маппинг в gRPC |
| `platform/http/middleware`, `platform/http/response` | цепочка middleware, запись JSON-конверта |
| `platform/grpc/interceptors` | цепочка интерсепторов |
| `order/internal/domain`, `service` | создание заказа и расчёт суммы (с моками) |
| `order/internal/event` | сериализация события в outbox |
| `notification/.../transport/kafka/order` | разбор payload события |

`-benchmem`, `-count=10`, сравнение `benchstat`. Порог значимых изменений — то, что `benchstat`
помечает как статистически значимое (p < 0.05); автоматического гейта нет, вывод читает человек.

## Baseline и отчёт (`load/results/baseline.md`)

Структура:

1. Окружение: CPU/RAM хоста, ОС, версии Docker, образов, Go, лимиты контейнеров, коммит.
2. Сводка: предел HTTP (RPS), предел каждого gRPC-метода, узкое место одной фразой.
3. Таблица ступеней stress: целевой RPS, достигнутый RPS, p50/p95/p99, ошибки, лаг p95, CPU сервисов.
4. Таблицы ghz по методам.
5. Узкое место: доказательства и ссылки на файлы в `raw/`, рекомендации.
6. Пороги `regression`, выведенные из baseline: RPS = 70% предела, p95 ≤ 1.5× p95 baseline на этом
   RPS, ошибки < 1%. Эти числа записываются в `k6/lib/config.js`.
7. Как воспроизвести: команды.

Перезапись baseline — только вручную и осознанно (новый отчёт с датой, старый — в git-истории).

## Изменения кода сервисов

- Бенчмарки — новые `_test.go` в существующих пакетах.
- Изменений поведения и конфигурации сервисов нет.

## Связь с профилированием

Профилирование — отдельная спека (pprof-эндпоинты в сервисах, автоснимки, continuous profiling).
`load` от неё не зависит и делается без неё. Чтобы профилирование потом встроилось без переделки:

- load-стек и задачи `load-*` не завязаны на конкретный набор контейнеров — добавить контейнеры
  профилирования и env сервисов можно правкой `load/compose.yaml`;
- задачи `load-stress`/`load-soak` размечают ступени (тег `stage` и время начала/конца ступени в
  summary) — по этим меткам профилирование будет снимать и сравнивать профили.

## Code Style

k6 — ES-модули, конфиг отделён от сценария:

```javascript
import http from 'k6/http';
import { check } from 'k6';
import { profile } from './lib/config.js';
import { loadSeed, randomOrder } from './lib/seed.js';

export const options = profile(__ENV.PROFILE);

export function setup() {
  return loadSeed();
}

export default function (seed) {
  const res = http.post(`${__ENV.GATEWAY_URL}/api/v1/orders`, JSON.stringify(randomOrder(seed)), {
    headers: { 'Content-Type': 'application/json' },
    tags: { name: 'create_order' },
  });
  check(res, {
    'status is 201': (r) => r.status === 201,
    'has order_id': (r) => r.json('data.order_id') !== undefined,
  });
}
```

Go-бенчмарк — по `test-foundation`, Go 1.24+ `b.Loop()`:

```go
func BenchmarkCreateOrderRequest_Validate(b *testing.B) {
	req := validCreateOrderRequest()

	for b.Loop() {
		if err := req.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}
```

Имена — `Benchmark<Тип>_<Метод>`, подбенчмарки `b.Run` для размеров входа (`items=1`, `items=10`).
Комментарии на русском.

## Testing Strategy

- `load-smoke` — проверка самих сценариев: запускается после любой правки `k6/` и перед `stress`.
- Бенчмарки в `task test` не запускаются (`-bench` не передаётся), но компилируются и проверяются
  `go vet` и линтером; `task bench` запускается вручную.
- Пороги `regression` проверяются тем, что задача падает на заведомо заниженном пороге (ручная
  проверка один раз, фиксируется в todo).

## Boundaries

- **Always:** фиксированные версии образов; лимиты ресурсов в load-стеке; записывать окружение в
  отчёт; `load-smoke` перед `stress`; `LOG_LEVEL=info` в load-стеке; чистая БД на каждый прогон.
- **Ask first:** оптимизация найденного узкого места; изменение лимитов ресурсов после baseline;
  изменение критерия предела; добавление метрик (например, пула pgx); перезапись `baseline.txt`/`baseline.md`.
- **Never:** гонять нагрузку по dev-стеку; коммитить гигабайты сырых данных; подгонять пороги `regression` под плохой прогон.

## Success Criteria

- [ ] `task load` поднимает стек, проходит smoke и stress, убирает стек одной командой.
- [ ] Stress доходит до деградации по записанному критерию (или до 2000 RPS — тогда это фиксируется как «предел не найден в диапазоне»).
- [ ] Предел HTTP и предел каждого из 4 gRPC-методов записаны в `baseline.md` с таблицами ступеней.
- [ ] Узкое место названо и подтверждено минимум двумя источниками (метрики/трейсы/docker stats).
- [ ] Лаг асинхронной цепочки измерен по ступеням, сказано, догоняет ли `notification`.
- [ ] Метрики k6 видны на дашборде `Load` в Grafana вместе с метриками сервисов во время прогона.
- [ ] `task load-regression` проходит на текущем коде и падает при заниженном пороге.
- [ ] `task bench` и `task bench-compare` работают, `baseline.txt` закоммичен, бенчмарки покрывают пакеты из таблицы.
- [ ] `task load-soak` проходит 30 мин; в отчёте — сравнение начала и конца по таблице «Soak» и вывод об утечках.
- [ ] Линтер зелёный, `task test` не замедлился (бенчмарки в нём не запускаются).

## Open Questions

Нет.

Решено: ступени `stress` — по разведочному прогону; критерий деградации — 1% / 3× / 95%;
метрики пула pgx уже есть, добавляются панели на дашборд `Load`; `soak` входит в модуль;
профилирование — отдельная спека, `load` от неё не зависит.
