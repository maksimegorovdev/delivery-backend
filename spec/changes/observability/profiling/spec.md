# Spec: profiling

> Status: **approved** · Depends on: [dashboards](../../../specs/observability/dashboards/spec.md), [testing/load](../../../specs/testing/map.md)

## Objective

Дать возможность снимать и смотреть профили Go-сервисов тремя способами:

1. **По запросу:** pprof-эндпоинт в каждом сервисе на отдельном debug-порту.
2. **Под нагрузкой:** профили снимаются автоматически в задачах `load-stress` и `load-soak`, сохраняются
   рядом с остальными результатами прогона и сравниваются с эталоном.
3. **Непрерывно:** Grafana Alloy (`pyroscope.scrape`) опрашивает pprof-эндпоинты и отправляет профили в
   Grafana Pyroscope; flame graph и сравнение периодов доступны в Grafana. Логи, метрики и трейсы по-прежнему
   идут через OTel Collector в ClickHouse.

Практические цели первой итерации:

- разобрать узкое место `order` под stress: гипотеза о нехватке пула pgx (`PG_MAX_CONNS`), CPU-throttling
  `user`/`product` на gRPC (открытые вопросы из [testing/map.md](../../../specs/testing/map.md));
- сравнивать профили «до/после» изменения (`go tool pprof -diff_base`) по сохранённому эталону;
- находить утечки горутин и памяти в soak: снимки `goroutine`/`heap` в начале и в конце прогона.

Пользователь — разработчик проекта, работающий локально (`task compose-up` + сервисы через Air) и на
изолированном стеке `delivery-load`.

### Acceptance criteria

1. **Пакет `platform/debugserver`.**
   - Отдельный `http.Server` с собственным `http.ServeMux` (не `http.DefaultServeMux`): отдаёт
     маршруты, как у `net/http/pprof` (`/debug/pprof/`, `cmdline`, `profile`, `symbol`, `trace` и именованные профили),
     но хендлеры написаны поверх `runtime/pprof` и `runtime/trace`. Пакет `net/http/pprof` не импортируется:
     его `init()` безусловно регистрирует профили на `http.DefaultServeMux`.
   - Создаётся только при непустом адресе; пустой `PPROF_ADDR` → сервер не стартует, сервис работает как раньше.
   - Таймауты: `ReadHeaderTimeout` задан; `WriteTimeout` больше максимальной длительности CPU-профиля и trace
     (обработчик pprof отклоняет `seconds`, превышающие `WriteTimeout`); `IdleTimeout` задан.
   - Опционально включает mutex- и block-профили: `runtime.SetMutexProfileFraction`, `runtime.SetBlockProfileRate`
     по конфигу; по умолчанию выключены (0).
   - Жизненный цикл как у `httpserver`/`grpcserver`: листенер открывается в `App.New` через `listener.Listen`
     (занятый порт — ошибка старта, закрытие регистрируется в `closer`); `Serve(ctx, lis)` запускается в `errgroup`
     в `App.Run` и по отмене `ctx` выполняет graceful shutdown с таймаутом; запуск и адрес логируются.
   - Не добавляет OTel-инструментацию (`otelhttp`) на debug-маршруты: их опрос не должен порождать спаны и RED-метрики.
2. **Подключение во всех пяти сервисах** (`gateway`, `order`, `user`, `product`, `notification`):
   - конфиг `Debug` в `internal/config`: `PPROF_ADDR` (необязательный), `PPROF_MUTEX_FRACTION`, `PPROF_BLOCK_RATE`
     (необязательные, `>= 0`, по умолчанию 0);
   - `.env.example` (и локальный `.env`) — `127.0.0.1:<порт>`, порты фиксированы:

     | Сервис | dev `PPROF_ADDR` | load: внутри контейнера → хост |
     |---|---|---|
     | gateway | `127.0.0.1:6060` | `:6060` → `127.0.0.1:36060` |
     | order | `127.0.0.1:6061` | `:6060` → `127.0.0.1:36061` |
     | user | `127.0.0.1:6062` | `:6060` → `127.0.0.1:36062` |
     | product | `127.0.0.1:6063` | `:6060` → `127.0.0.1:36063` |
     | notification | `127.0.0.1:6064` | `:6060` → `127.0.0.1:36064` |
   - debug-порт не совпадает с портом публичного API и не публикуется наружу шире `127.0.0.1`.
3. **Снимки по запросу:** корневая задача `task pprof SERVICE=<svc> [TYPE=profile|heap|allocs|goroutine|mutex|block|trace] [SECONDS=30]`
   сохраняет профиль в `tmp/pprof/<service>-<type>-<ts>.pb.gz` (для `trace` — `.trace`) и печатает команду
   `go tool pprof -http=:0 <файл>` для просмотра. Неизвестный сервис или тип → понятная ошибка, ненулевой код.
4. **Автоснимки в load-стеке.**
   - В `load/compose.yaml` у всех сервисов `PPROF_ADDR=:6060`, порты по таблице AC2; у `order`, `user`, `product`
     включены `PPROF_MUTEX_FRACTION` и `PPROF_BLOCK_RATE` (значения — в Code Style).
   - Скрипт `load/scripts/pprof-snapshots.sh` — фоновый сэмплер в стиле `docker-stats.sh`: каждые `N` секунд снимает
     для каждого целевого сервиса CPU (`profile?seconds=<N-5>`), `heap`, `goroutine`, `mutex`, `block` в
     `results/raw/<run>/pprof/<service>/<HHMMSS>-<type>.pb.gz`. Целевые сервисы — `PPROF_TARGETS`
     (по умолчанию `order,user,product,gateway,notification`; `notification` — асинхронный путь через Kafka,
     на нём в stress растёт лаг). Ошибка одного снимка пишется в `pprof/errors.log` и не роняет сэмплер.
   - `load-stress` запускает сэмплер с периодом 30 с: каждой ступени RPS соответствует хотя бы один CPU-профиль;
     время снимка сопоставляется со ступенью по `summary.json`.
   - `load-soak` снимает `heap` и `goroutine?debug=1` + `goroutine` (pb) всех целевых сервисов **в начале и в конце**
     (рядом с `soak-start.txt`/`soak-end.txt`: `pprof/<service>/soak-{start,end}-<type>`) и запускает сэмплер с периодом 300 с.
   - Сэмплер гасится через `defer`, как `docker-stats.sh`/`group-lag.sh`.
5. **Сравнение с эталоном.**
   - Профили, на которые ссылается `load/results/baseline.md`, копируются в `load/results/baseline/pprof/`
     (как остальные артефакты эталона; `results/raw` не коммитится).
   - Задача `task load-pprof-diff SERVICE=<svc> TYPE=<type> [RUN=<dir>]` открывает `go tool pprof -http=:0
     -diff_base=<эталон> <профиль прогона>` (по умолчанию — последний прогон и CPU-профиль на пиковой ступени).
   - Задача `task load-soak-leaks [RUN=<dir>]` печатает по каждому сервису число горутин в начале/конце soak и
     топ роста `inuse_space` (`go tool pprof -top -diff_base=soak-start-heap soak-end-heap`).
6. **Continuous profiling: Grafana Alloy (`pyroscope.scrape`) → Grafana Pyroscope.**
   - **Проверка (spike) — первая задача плана.** На `grafana/alloy:v1.21.3` и `grafana/pyroscope:2.4.2` подтвердить:
     (a) `pyroscope.scrape` опрашивает pprof-эндпоинт на хосте через `host.docker.internal`; (b) в Pyroscope у сервиса
     раздельные типы: CPU, память (heap), горутины, mutex, block; (c) `external_labels = { env = ... }` даёт label
     `env`, а heap не плодит label `bytes`; (d) в Grafana (датасорс Pyroscope, Explore и Profiles Drilldown) видны
     `service_name`, типы, flame graph и diff. Если пункт не проходит — остановиться и спросить пользователя.
   - В `compose.yaml` добавлены `pyroscope` (single binary, данные в volume, порт `127.0.0.1:4040`) и `alloy`
     (`extra_hosts: host.docker.internal:host-gateway`, конфиг смонтирован read-only, UI на `127.0.0.1:12345`).
   - `deploy/alloy/profiles.dev.alloy`: по одному `pyroscope.scrape "<svc>"` на сервис (цель
     `host.docker.internal:606x`, `service_name=<svc>`, `scrape_interval = "15s"`, все 5 типов в `profiling_config`),
     один `pyroscope.write` → `http://pyroscope:4040` с `external_labels = { env = "dev" }`.
     Недоступный сервис (не запущен) даёт ошибки скрейпа в логах Alloy, но не мешает остальным.
   - OTel Collector и его `config.yaml` не меняются.
   - В load-стеке профилирование подключается **override-файлом** `load/compose.profiling.yaml`: добавляет `pyroscope`
     и `alloy` с `deploy/alloy/profiles.load.alloy` (цели по именам контейнеров `order:6060`, …, `env = "load"`).
     По умолчанию выключено, чтобы не искажать замеры. Включение: `LOAD_PROFILING=1 task load-up`.
   - Grafana: датасорс Pyroscope (`uid: pyroscope`) провиженится рядом с ClickHouse; в Explore и в приложении
     Profiles Drilldown видны все сервисы, flame graph и сравнение двух интервалов (diff view) работают.
   - Образы `grafana/pyroscope` и `grafana/alloy` с фиксированными тегами; одинаковые в `compose.yaml` и
     `load/compose.profiling.yaml`; `images-check` учитывает override-файл.
   - **Результат spike на Alloy (2026-11-03, `grafana/alloy:v1.21.3`, `grafana/pyroscope:2.4.2`, `grafana/grafana:13.2.4`): пройден.**
     - (a) ✅ `alloy validate` проходит; `pyroscope.scrape` опрашивает хост через `host.docker.internal`, компонент `healthy`,
       ошибок в логах нет. Аргументы: `targets` (`__address__`, `service_name`), `scrape_interval = "15s"`,
       `forward_to`, `profiling_config { profile.process_cpu | memory | goroutine | mutex | block { enabled } }`;
       `pyroscope.write { endpoint { url }, external_labels }`. Feature gates не нужны.
     - (b) ✅ типы у `order` раздельные: `process_cpu`, `memory` (alloc/inuse), `goroutine`, `mutex`, `block`;
       flame graph через `/pyroscope/render` получен для каждого (mutex и block — отдельные ряды).
     - (c) ✅ `external_labels = { env = "dev" }` даёт label `env`; label `bytes` не появляется; в списке label'ов только
       `service_name`, `env`, `instance` и служебные `__*__`.
     - (d) ✅ датасорс `grafana-pyroscope-datasource` (`uid: pyroscope`, `http://pyroscope:4040`) здоров через
       `/api/datasources/uid/pyroscope/health`; запросы через прокси Grafana возвращают `service_name` и типы;
       приложение `grafana-pyroscope-app` 3.1.0 включено; diff двух интервалов отвечает через `/pyroscope/render-diff`.
       Не проверено глазами: отрисовка в UI Explore / Profiles Drilldown (проверка — в Task 8).
     - Нюанс Pyroscope 2.x: первые данные появились через ~60 с после старта; запросы раньше возвращают `{}`.
   - **Предыстория (spike 2026-11-03 на OTel Collector `0.163.2`, `pprofreceiver v0.163.0`, Pyroscope `2.4.2`):**
     вариант отклонён. Приём OTLP-профилей работает (CPU, goroutine, heap), но (1) mutex и block неразличимы: у
     `/debug/pprof/mutex` и `/debug/pprof/block` одинаковые `PeriodType: contentions count` и типы сэмплов, в
     Pyroscope оба попадают в тип `contentions:*`; (2) из resource-атрибутов label'ами становятся только dotless-ключи
     (`env` да, `deployment.environment.name`, `service.namespace`, `service.version` нет); (3) heap даёт label
     `bytes` (серия на размер аллокации). Пользователь выбрал Alloy.
7. **Отступление от «только ClickHouse» фиксируется** в разделе «Принятые решения» [map.md](../../../specs/observability/map.md):
   профили хранятся в Pyroscope, потому что в ClickHouse-экспортёре OTel Collector нет сигнала profiles,
   а сам сигнал в OTel ещё не стабилен (Alpha). Логи, метрики, трейсы остаются в ClickHouse. Там же — второе решение:
   профили собирает Grafana Alloy (`pyroscope.scrape`), а не OTel Collector, по итогам spike (см. AC6).
8. **Разбор узкого места `order`** — результат модуля, а не только инструмент: прогон `load-stress` с сэмплером,
   вывод (подтверждена/опровергнута гипотеза пула pgx, где CPU у `user`/`product`) с flame graph / `top` и ссылками
   на профили записан в `load/results/baseline.md`, профили — в `load/results/baseline/pprof/`.

## Tech Stack

- Go (версия из `go.work`), stdlib `runtime/pprof`, `runtime/trace`, `runtime`. **Без новых Go-зависимостей.**
- Grafana Alloy `v1.21.3` (новый контейнер): компоненты `pyroscope.scrape`, `pyroscope.write`.
- Grafana Pyroscope `2.4.2` (single binary) — новый контейнер, порт `127.0.0.1:4040`.
- OTel Collector contrib `0.163.2` — без изменений, профили не обрабатывает.
- Grafana `13.2.4` (уже есть): встроенный датасорс `grafana-pyroscope-datasource`, приложение Profiles Drilldown.
- Taskfile, POSIX sh, `curl`, `go tool pprof`.

## Commands

```bash
# dev
task compose-up                                   # + pyroscope, alloy
task watch-all                                    # сервисы с PPROF_ADDR из .env
task pprof SERVICE=order TYPE=profile SECONDS=30  # снимок в tmp/pprof/, печатает команду просмотра
go tool pprof -http=:0 tmp/pprof/order-profile-<ts>.pb.gz
curl -s 127.0.0.1:6061/debug/pprof/ | head       # эндпоинт жив
docker logs alloy 2>&1 | grep -i error        # ошибки скрейпа профилей
open http://localhost:3001/a/grafana-pyroscope-app # Profiles Drilldown

# load
task load-up                                      # без Pyroscope и Alloy
LOAD_PROFILING=1 task load-up                     # + load/compose.profiling.yaml (pyroscope, alloy)
task load-stress                                  # + results/raw/<run>/pprof/
task load-soak                                    # + soak-start/end снимки и сэмплер
task load-pprof-diff SERVICE=order TYPE=profile   # diff с эталоном
task load-soak-leaks                              # горутины и heap: начало vs конец soak

# качество
task test        # unit
task test-integration  # integration
task lint
```

## Project Structure

```
platform/debugserver/
  server.go            → New(addr, opts...), Serve(ctx, lis); собственный ServeMux с pprof
  options.go           → WithMutexProfileFraction, WithBlockProfileRate, WithLogger, WithTimeouts
  server_test.go       → httptest/реальный listener на 127.0.0.1:0
services/<svc>/internal/config/config.go  → Debug{Addr, MutexFraction, BlockRate}
services/<svc>/internal/app/app.go        → при непустом адресе: listener.Listen + debugserver.New, Serve в errgroup
services/<svc>/.env.example               → PPROF_ADDR=127.0.0.1:606x
deploy/otel-collector/config.yaml         → без изменений
deploy/alloy/profiles.dev.alloy           → pyroscope.scrape на host.docker.internal:6060..6064, pyroscope.write, env=dev
deploy/alloy/profiles.load.alloy          → то же по именам контейнеров, env=load
deploy/pyroscope/config.yaml              → (если нужен) хранение в volume, ретенция
deploy/grafana/provisioning/datasources/  → + pyroscope.yaml (uid pyroscope)
compose.yaml                              → pyroscope и alloy (extra_hosts, порты на 127.0.0.1)
load/compose.yaml                         → PPROF_* и порты у сервисов
load/compose.profiling.yaml               → pyroscope + alloy с profiles.load.alloy
load/scripts/pprof-snapshots.sh           → фоновый сэмплер профилей
load/Taskfile.yml, Taskfile.yml           → pprof, load-pprof-diff, load-soak-leaks; сэмплер в stress/soak
load/results/baseline/pprof/              → эталонные профили (коммитятся)
spec/specs/observability/map.md           → решение о Pyroscope, статус модуля
README.md                                 → раздел Observability: профилирование
```

## Code Style

Следует стилю `platform/http/httpserver`: функциональные опции, сервер не знает о конфиге сервиса,
ошибки оборачиваются с контекстом, doc-комментарии на русском.

```go
// Server — HTTP-сервер отладочных эндпоинтов (pprof) на отдельном порту.
// Использует собственный ServeMux и не импортирует net/http/pprof: его init()
// регистрирует профили на http.DefaultServeMux, который можно отдать наружу по ошибке.
type Server struct {
	srv *http.Server
	log *slog.Logger
}

func New(addr string, opts ...Option) *Server {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	runtime.SetMutexProfileFraction(o.mutexFraction)
	runtime.SetBlockProfileRate(o.blockRate)

	return &Server{
		srv: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: o.readHeaderTimeout, // 5s
			WriteTimeout:      o.writeTimeout,      // 65s: CPU-профиль и trace до 60s
			IdleTimeout:       o.idleTimeout,       // 60s
		},
		log: o.log,
	}
}
```

- Значения в load-стеке: `PPROF_MUTEX_FRACTION=5`, `PPROF_BLOCK_RATE=10000` (нс; событие блокировки ≥10 мкс
  попадает в профиль с вероятностью 1). В dev — 0, Alloy получает пустые mutex/block-профили.
- Имена файлов профилей: `<HHMMSS>-<type>.pb.gz`, `soak-<label>-<type>.pb.gz`; тип — как имя профиля pprof.
- Shell-скрипты — POSIX `sh`, `set -eu`, как `docker-stats.sh`; комментарии на русском.
- Конфиг Alloy для профилей — отдельный файл на окружение (`profiles.dev.alloy`, `profiles.load.alloy`); компоненты
  `pyroscope.scrape "<svc>"`, один `pyroscope.write "pyroscope"`; окружение — `external_labels = { env = ... }`.
  Точные аргументы сверяются с документацией `pyroscope.scrape` той версии Alloy, что в compose.

Пример (dev, один сервис; остальные — по тому же шаблону):

```alloy
pyroscope.write "pyroscope" {
  endpoint { url = "http://pyroscope:4040" }
  external_labels = { env = "dev" }
}

pyroscope.scrape "order" {
  targets         = [{ "__address__" = "host.docker.internal:6061", "service_name" = "order" }]
  scrape_interval = "15s"
  forward_to      = [pyroscope.write.pyroscope.receiver]

  profiling_config {
    profile.process_cpu { enabled = true }  // delta; seconds = scrape_interval - 1
    profile.memory      { enabled = true }
    profile.goroutine   { enabled = true }
    profile.mutex       { enabled = true }
    profile.block       { enabled = true }
  }
}
```

## Testing Strategy

| Уровень | Что | Как |
|---|---|---|
| Unit `platform/debugserver` | `/debug/pprof/` отвечает 200 и содержит список профилей; `goroutine?debug=1`, `heap` отдают данные; `profile?seconds=1` работает; `seconds` больше `WriteTimeout` → ошибка обработчика | реальный listener `127.0.0.1:0`, table-driven, `t.Parallel()` |
| Unit `platform/debugserver` | неизвестный путь → 404; маршрутов вне `/debug/pprof/` нет; `DefaultServeMux` не используется (профили не появляются на другом сервере в том же процессе) | второй `httptest.Server` с `http.DefaultServeMux` → 404 на `/debug/pprof/` |
| Unit `platform/debugserver` | `Serve` завершается graceful shutdown по отмене ctx и возвращает ошибку закрытого/занятого листенера; без утечек горутин | `goleak` |
| Unit config сервисов | `PPROF_ADDR` пустой → `Debug.Addr == ""`; отрицательные rate/fraction → ошибка валидации | существующие `config_test.go` |
| Integration (app) | при заданном `PPROF_ADDR` эндпоинт отвечает, при пустом порт не слушается | существующие `app_integration_test.go`, где они есть |
| Spike | пункты (a)–(d) AC6 на Alloy и Pyroscope | `docker compose up` временного стека, запрос в Pyroscope API и Grafana Explore |
| Конфиг | Alloy стартует с конфигом без ошибок | `alloy validate` (или `alloy fmt --test`) в контейнере; UI `127.0.0.1:12345`, все `pyroscope.scrape` healthy |
| Ручная dev | в Grafana Profiles Drilldown 5 сервисов со всеми типами профилей (mutex и block раздельно), flame graph CPU под `ab`-нагрузкой; в логах Alloy нет ошибок скрейпа для запущенных сервисов | `task compose-up`, `task watch-all` |
| Ручная load | после `load-stress` в `results/raw/<run>/pprof/` есть CPU-профиль на каждую ступень; после `load-soak` — `soak-start/end` снимки, `load-soak-leaks` печатает таблицу | `task load` / `LOAD_KEEP=1` |
| Регрессия | `task load-regression` с сэмплером выключен и без профиля `profiling` даёт p95 в пределах эталона (профилирование по умолчанию не влияет на замеры) | сравнение с `baseline.md` |

Покрытие `platform/debugserver` — не ниже порога `.testcoverage.yml` платформы.

## Boundaries

- **Always:** debug-порт отдельный от публичного API и привязан к `127.0.0.1` на хосте; `PPROF_ADDR` пустой →
  сервер выключен; фиксировать теги образов Pyroscope и Alloy; профили эталона класть в `load/results/baseline/pprof/`
  вместе с записью в `baseline.md`; проверять `task lint` и тесты перед сдачей.
- **Ask first:** изменения основного
  `deploy/otel-collector/config.yaml`; новые Go-зависимости (`pyroscope-go`, `otel-profiling-go`, `fgprof`);
  push-режим или span profiles; включение профилирования в load-стеке по умолчанию; ненулевые mutex/block rate в dev; дашборд Grafana
  с профилями в `deploy/grafana/dashboards`; ретенция Pyroscope больше дефолтной; изменения `e2e/compose.yaml`.
- **Never:** регистрировать pprof на `http.DefaultServeMux` или на публичном HTTP gateway; публиковать debug-порт
  на `0.0.0.0` хоста; коммитить `load/results/raw/`; менять код горячих путей «по профилю» в рамках этого модуля
  без отдельного решения (найденные проблемы — в выводы `baseline.md` и `TODO.md`).

## Success Criteria

- [ ] Spike AC6 (a)–(d) на Alloy пройден; результат и версии записаны в спеку.
- [ ] `platform/debugserver` с тестами; покрытие не ниже порога; `goleak` чистый.
- [ ] Во всех 5 сервисах при `PPROF_ADDR` из `.env.example` `curl 127.0.0.1:606x/debug/pprof/` → 200; без `PPROF_ADDR` порт не слушается.
- [ ] `task pprof SERVICE=order` создаёт открываемый `go tool pprof` профиль.
- [ ] `task load-stress` кладёт CPU/heap/goroutine/mutex/block-профили всех пяти сервисов по ступеням в `results/raw/<run>/pprof/`.
- [ ] `task load-soak` кладёт снимки начала/конца; `task load-soak-leaks` показывает рост горутин и `inuse_space`.
- [ ] `task load-pprof-diff` открывает diff с эталоном из `load/results/baseline/pprof/`.
- [ ] После `task compose-up` + `task watch-all` в Grafana (датасорс Pyroscope) видны профили всех 5 сервисов, diff двух интервалов работает.
- [ ] `LOAD_PROFILING=1 task load-up` поднимает Pyroscope и Alloy в load-стеке; без флага их нет; `images-check` проходит.
- [ ] В `baseline.md` записан разбор узкого места `order` (пул pgx, CPU `user`/`product`) со ссылками на профили.
- [ ] `map.md`: решение о Pyroscope в «Принятых решениях», модуль `profiling` — `done`; README обновлён.

## Assumptions

1. Сервисы в dev запускаются через Air на хосте; Alloy в Docker достаёт их через `host.docker.internal`
   (Docker Desktop на macOS). На Linux нужен `extra_hosts: host.docker.internal:host-gateway` — добавляется сразу.
2. Pull-режим (Alloy опрашивает pprof) выбран, чтобы в коде сервисов не было ничего, кроме debug-сервера.
3. Отдельный агент (Alloy) принят как цена за раздельные типы профилей и зрелый путь в Pyroscope; OTel Collector
   и его основной конфиг профилями не затрагиваются. При поломке профилирование отключается удалением сервиса `alloy`.
4. Профили — локальное средство разработки; аутентификации на debug-порту нет, защита — привязка к `127.0.0.1`.
5. Ретенция Pyroscope — дефолтная; данные теряются с `docker compose down -v`.

## Open Questions

Нет.

## Решённые вопросы

1. Целевые сервисы сэмплера по умолчанию — все пять, включая `notification`.
2. Связь профилей с трейсами (span profiles, кнопка «профиль спана» в Grafana) — **вне скоупа**. Штатная кнопка
   есть только у датасорса Tempo и требует обёртки `otel-profiling-go` в `platform/telemetry`; трейсы у нас в
   ClickHouse. Профили и трейсы сопоставляются вручную по `service_name` и интервалу времени.
3. Сбор профилей — через Grafana Alloy (`pyroscope.scrape` → `pyroscope.write` → Pyroscope), а не через OTel Collector.
   Spike показал, что `pprofreceiver` (OTLP profiles) сливает mutex и block в один тип `contentions`, отбрасывает
   resource-атрибуты кроме dotless-ключей и плодит label `bytes` на heap. Решение принято пользователем 2026-11-03.
