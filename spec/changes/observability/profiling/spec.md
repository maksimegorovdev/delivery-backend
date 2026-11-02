# Spec: profiling

> Status: **approved** · Depends on: [dashboards](../../../specs/observability/dashboards/spec.md), [testing/load](../../../specs/testing/map.md)

## Objective

Дать возможность снимать и смотреть профили Go-сервисов тремя способами:

1. **По запросу:** pprof-эндпоинт в каждом сервисе на отдельном debug-порту.
2. **Под нагрузкой:** профили снимаются автоматически в задачах `load-stress` и `load-soak`, сохраняются
   рядом с остальными результатами прогона и сравниваются с эталоном.
3. **Непрерывно:** существующий OTel Collector опрашивает pprof-эндпоинты (`pprofreceiver`) и отправляет
   профили четвёртым сигналом OTel (OTLP profiles) в Grafana Pyroscope; flame graph и сравнение периодов
   доступны в Grafana. Все сигналы идут через один Collector, отдельного агента нет.

Практические цели первой итерации:

- разобрать узкое место `order` под stress: гипотеза о нехватке пула pgx (`PG_MAX_CONNS`), CPU-throttling
  `user`/`product` на gRPC (открытые вопросы из [testing/map.md](../../../specs/testing/map.md));
- сравнивать профили «до/после» изменения (`go tool pprof -diff_base`) по сохранённому эталону;
- находить утечки горутин и памяти в soak: снимки `goroutine`/`heap` в начале и в конце прогона.

Пользователь — разработчик проекта, работающий локально (`task compose-up` + сервисы через Air) и на
изолированном стеке `delivery-load`.

### Acceptance criteria

1. **Пакет `platform/debugserver`.**
   - Отдельный `http.Server` с собственным `http.ServeMux` (не `http.DefaultServeMux`): регистрирует
     `net/http/pprof` (`/debug/pprof/`, `cmdline`, `profile`, `symbol`, `trace` и именованные профили через `Index`).
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
6. **Continuous profiling: OTel Collector (`pprofreceiver`) → OTLP → Grafana Pyroscope.**
   - **Проверка (spike) — первая задача плана.** На образе `otel/opentelemetry-collector-contrib:0.163.2` подтвердить:
     (a) в сборке есть `pprofreceiver` с режимом `remote`; (b) профильный pipeline работает с feature gate
     `service.profilesSupport`; (c) процессор `resource` применяется к профилям; (d) Pyroscope принимает OTLP-профили
     от Collector, в Grafana видны `service_name` и типы профилей (CPU, память, горутины, mutex, block).
     Если нужна более новая версия Collector — обновить тег во всех compose-файлах (`images-check`). Если хотя бы
     пункт (a)–(d) не проходит — остановиться и согласовать запасной вариант (Grafana Alloy `pyroscope.scrape`):
     debugserver, эндпоинты и AC1–AC5 от этого не меняются.
   - В `compose.yaml` добавлен `pyroscope` (single binary, данные в volume, порт `127.0.0.1:4040`, OTLP gRPC на
     том же порту).
   - Профильная часть конфига Collector — **отдельные файлы**, которые сливаются с основным конфигом
     (`--config=config.yaml --config=profiles.dev.yaml`): основной `config.yaml` (traces/metrics/logs → ClickHouse)
     не меняется. Collector запускается с `--feature-gates=service.profilesSupport`.
   - `deploy/otel-collector/profiles.dev.yaml`: по одному `pprof/<svc>_<type>` receiver'у в режиме `remote` на
     каждую пару сервис × тип (5 × 5: `profile?seconds=10`, `heap`, `goroutine`, `mutex`, `block`) с адресом
     `host.docker.internal:606x` (сервисы запущены через Air на хосте), `collection_interval: 15s`; один pipeline
     `profiles/<svc>` на сервис с процессором `resource/<svc>` (`service.name=<svc>`, `deployment.environment.name=dev`),
     экспорт `otlp/pyroscope` → `pyroscope:4040`. Повторяющиеся блоки — через YAML-якоря.
     Недоступный сервис (не запущен) даёт ошибки скрейпа в логах Collector, но не останавливает его и другие pipeline'ы.
   - У `otel-collector` в compose `extra_hosts: host.docker.internal:host-gateway` (Linux).
   - В load-стеке профилирование подключается **override-файлом** `load/compose.profiling.yaml`: добавляет `pyroscope`
     и переопределяет команду `otel-collector` (`--config=…/profiles.load.yaml`, feature gate). По умолчанию выключено,
     чтобы не искажать замеры. `profiles.load.yaml` опрашивает сервисы по именам контейнеров (`order:6060`, …),
     `deployment.environment.name=load`. Включение: `LOAD_PROFILING=1 task load-up`.
   - Grafana: датасорс Pyroscope (`uid: pyroscope`) провиженится рядом с ClickHouse; в Explore и в приложении
     Profiles Drilldown видны все сервисы, flame graph и сравнение двух интервалов (diff view) работают.
   - Образ `grafana/pyroscope` с фиксированным тегом; одинаковый в `compose.yaml` и `load/compose.profiling.yaml`;
     `images-check` учитывает override-файл.
7. **Отступление от «только ClickHouse» фиксируется** в разделе «Принятые решения» [map.md](../../../specs/observability/map.md):
   профили хранятся в Pyroscope, потому что в ClickHouse-экспортёре OTel Collector нет сигнала profiles,
   а сам сигнал в OTel ещё не стабилен (Alpha). Логи, метрики, трейсы остаются в ClickHouse. Там же — второе решение:
   профили собирает тот же Collector (`pprofreceiver`, feature gate `service.profilesSupport`), без отдельного агента.
8. **Разбор узкого места `order`** — результат модуля, а не только инструмент: прогон `load-stress` с сэмплером,
   вывод (подтверждена/опровергнута гипотеза пула pgx, где CPU у `user`/`product`) с flame graph / `top` и ссылками
   на профили записан в `load/results/baseline.md`, профили — в `load/results/baseline/pprof/`.

## Tech Stack

- Go (версия из `go.work`), stdlib `net/http/pprof`, `runtime`. **Без новых Go-зависимостей.**
- OTel Collector contrib `0.163.2` (уже есть): `pprofreceiver` (Alpha), процессор `resource`, экспортёр OTLP gRPC,
  feature gate `service.profilesSupport`. Версия может подняться по итогам spike (AC6).
- Grafana Pyroscope (single binary) — новый контейнер; тег фиксируется на последнем стабильном релизе на момент
  реализации и записывается в эту спеку.
- Grafana `13.2.4` (уже есть): встроенный датасорс `grafana-pyroscope-datasource`, приложение Profiles Drilldown.
- Taskfile, POSIX sh, `curl`, `go tool pprof`.

## Commands

```bash
# dev
task compose-up                                   # + pyroscope, профильные pipeline'ы Collector
task watch-all                                    # сервисы с PPROF_ADDR из .env
task pprof SERVICE=order TYPE=profile SECONDS=30  # снимок в tmp/pprof/, печатает команду просмотра
go tool pprof -http=:0 tmp/pprof/order-profile-<ts>.pb.gz
curl -s 127.0.0.1:6061/debug/pprof/ | head       # эндпоинт жив
docker logs otel-collector 2>&1 | grep -i pprof   # ошибки скрейпа профилей
open http://localhost:3001/a/grafana-pyroscope-app # Profiles Drilldown

# load
task load-up                                      # без Pyroscope и профильных pipeline'ов
LOAD_PROFILING=1 task load-up                     # + load/compose.profiling.yaml
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
deploy/otel-collector/config.yaml         → без изменений (traces/metrics/logs → ClickHouse)
deploy/otel-collector/profiles.dev.yaml   → pprof receivers host.docker.internal:6060..6064, profiles/<svc>, otlp/pyroscope
deploy/otel-collector/profiles.load.yaml  → то же по именам контейнеров, env=load
deploy/pyroscope/config.yaml              → (если нужен) хранение в volume, ретенция
deploy/grafana/provisioning/datasources/  → + pyroscope.yaml (uid pyroscope)
compose.yaml                              → pyroscope; otel-collector: второй --config, feature gate, extra_hosts
load/compose.yaml                         → PPROF_* и порты у сервисов
load/compose.profiling.yaml               → pyroscope + переопределение команды otel-collector
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
// Использует собственный ServeMux: импорт net/http/pprof в других пакетах
// не публикует профили на публичных серверах.
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
  попадает в профиль с вероятностью 1). В dev — 0, Collector получает пустые mutex/block-профили.
- Имена файлов профилей: `<HHMMSS>-<type>.pb.gz`, `soak-<label>-<type>.pb.gz`; тип — как имя профиля pprof.
- Shell-скрипты — POSIX `sh`, `set -eu`, как `docker-stats.sh`; комментарии на русском.
- Конфиг Collector для профилей — отдельный файл на окружение; имена компонентов `pprof/<svc>_<type>`,
  `resource/<svc>`, `profiles/<svc>`; повторы — через YAML-якоря. Точные ключи сверяются с README `pprofreceiver`
  той версии Collector, что в compose (компонент Alpha, ключи могут меняться).

Пример (dev, один сервис; остальные — по тому же шаблону):

```yaml
x-pprof-remote: &pprof-remote
  collection_interval: 15s
  initial_delay: 5s

receivers:
  pprof/order_cpu:       { remote: { <<: *pprof-remote, endpoint: "http://host.docker.internal:6061/debug/pprof/profile?seconds=10" } }
  pprof/order_heap:      { remote: { <<: *pprof-remote, endpoint: "http://host.docker.internal:6061/debug/pprof/heap" } }
  pprof/order_goroutine: { remote: { <<: *pprof-remote, endpoint: "http://host.docker.internal:6061/debug/pprof/goroutine" } }
  pprof/order_mutex:     { remote: { <<: *pprof-remote, endpoint: "http://host.docker.internal:6061/debug/pprof/mutex" } }
  pprof/order_block:     { remote: { <<: *pprof-remote, endpoint: "http://host.docker.internal:6061/debug/pprof/block" } }

processors:
  resource/order:
    attributes:
      - { key: service.name, value: order, action: upsert }
      - { key: deployment.environment.name, value: dev, action: upsert }

exporters:
  otlp/pyroscope:
    endpoint: pyroscope:4040
    tls: { insecure: true }

service:
  pipelines:
    profiles/order:
      receivers: [pprof/order_cpu, pprof/order_heap, pprof/order_goroutine, pprof/order_mutex, pprof/order_block]
      processors: [memory_limiter, resource/order]
      exporters: [otlp/pyroscope]
```

## Testing Strategy

| Уровень | Что | Как |
|---|---|---|
| Unit `platform/debugserver` | `/debug/pprof/` отвечает 200 и содержит список профилей; `goroutine?debug=1`, `heap` отдают данные; `profile?seconds=1` работает; `seconds` больше `WriteTimeout` → ошибка обработчика | реальный listener `127.0.0.1:0`, table-driven, `t.Parallel()` |
| Unit `platform/debugserver` | неизвестный путь → 404; маршрутов вне `/debug/pprof/` нет; `DefaultServeMux` не используется (профили не появляются на другом сервере в том же процессе) | второй `httptest.Server` с `http.DefaultServeMux` → 404 на `/debug/pprof/` |
| Unit `platform/debugserver` | `Serve` завершается graceful shutdown по отмене ctx и возвращает ошибку закрытого/занятого листенера; без утечек горутин | `goleak` |
| Unit config сервисов | `PPROF_ADDR` пустой → `Debug.Addr == ""`; отрицательные rate/fraction → ошибка валидации | существующие `config_test.go` |
| Integration (app) | при заданном `PPROF_ADDR` эндпоинт отвечает, при пустом порт не слушается | существующие `app_integration_test.go`, где они есть |
| Spike | пункты (a)–(d) AC6 на версии Collector из compose | `docker run … --feature-gates=service.profilesSupport` с `profiles.dev.yaml`, запрос в Pyroscope API / Grafana |
| Конфиг | Collector стартует с объединённым конфигом без ошибок валидации | `otelcol-contrib validate --config=… --config=…` в контейнере; healthcheck `13133` |
| Ручная dev | в Grafana Profiles Drilldown 5 сервисов со всеми типами профилей, flame graph CPU под `ab`-нагрузкой; в логах Collector нет ошибок скрейпа для запущенных сервисов | `task compose-up`, `task watch-all` |
| Ручная load | после `load-stress` в `results/raw/<run>/pprof/` есть CPU-профиль на каждую ступень; после `load-soak` — `soak-start/end` снимки, `load-soak-leaks` печатает таблицу | `task load` / `LOAD_KEEP=1` |
| Регрессия | `task load-regression` с сэмплером выключен и без профиля `profiling` даёт p95 в пределах эталона (профилирование по умолчанию не влияет на замеры) | сравнение с `baseline.md` |

Покрытие `platform/debugserver` — не ниже порога `.testcoverage.yml` платформы.

## Boundaries

- **Always:** debug-порт отдельный от публичного API и привязан к `127.0.0.1` на хосте; `PPROF_ADDR` пустой →
  сервер выключен; фиксировать теги образов Pyroscope и Collector; профили эталона класть в `load/results/baseline/pprof/`
  вместе с записью в `baseline.md`; проверять `task lint` и тесты перед сдачей.
- **Ask first:** переход на запасной вариант с Alloy, если spike (AC6) не прошёл; изменения основного
  `deploy/otel-collector/config.yaml`; новые Go-зависимости (`pyroscope-go`, `otel-profiling-go`, `fgprof`);
  push-режим или span profiles; включение профилирования в load-стеке по умолчанию; ненулевые mutex/block rate в dev; дашборд Grafana
  с профилями в `deploy/grafana/dashboards`; ретенция Pyroscope больше дефолтной; изменения `e2e/compose.yaml`.
- **Never:** регистрировать pprof на `http.DefaultServeMux` или на публичном HTTP gateway; публиковать debug-порт
  на `0.0.0.0` хоста; коммитить `load/results/raw/`; менять код горячих путей «по профилю» в рамках этого модуля
  без отдельного решения (найденные проблемы — в выводы `baseline.md` и `TODO.md`).

## Success Criteria

- [ ] Spike AC6 (a)–(d) пройден на версии Collector из compose; результат и версии записаны в спеку.
- [ ] `platform/debugserver` с тестами; покрытие не ниже порога; `goleak` чистый.
- [ ] Во всех 5 сервисах при `PPROF_ADDR` из `.env.example` `curl 127.0.0.1:606x/debug/pprof/` → 200; без `PPROF_ADDR` порт не слушается.
- [ ] `task pprof SERVICE=order` создаёт открываемый `go tool pprof` профиль.
- [ ] `task load-stress` кладёт CPU/heap/goroutine/mutex/block-профили всех пяти сервисов по ступеням в `results/raw/<run>/pprof/`.
- [ ] `task load-soak` кладёт снимки начала/конца; `task load-soak-leaks` показывает рост горутин и `inuse_space`.
- [ ] `task load-pprof-diff` открывает diff с эталоном из `load/results/baseline/pprof/`.
- [ ] После `task compose-up` + `task watch-all` в Grafana (датасорс Pyroscope) видны профили всех 5 сервисов, diff двух интервалов работает.
- [ ] `LOAD_PROFILING=1 task load-up` поднимает Pyroscope и профильные pipeline'ы Collector в load-стеке; без флага их нет; `images-check` проходит.
- [ ] В `baseline.md` записан разбор узкого места `order` (пул pgx, CPU `user`/`product`) со ссылками на профили.
- [ ] `map.md`: решение о Pyroscope в «Принятых решениях», модуль `profiling` — `done`; README обновлён.

## Assumptions

1. Сервисы в dev запускаются через Air на хосте; Collector в Docker достаёт их через `host.docker.internal`
   (Docker Desktop на macOS). На Linux нужен `extra_hosts: host.docker.internal:host-gateway` — добавляется сразу.
2. Pull-режим (Collector опрашивает pprof) выбран, чтобы в коде сервисов не было ничего, кроме debug-сервера.
3. Alpha-статус сигнала profiles и `pprofreceiver` приемлем: стек локальный и учебный, основной конфиг Collector
   отделён от профильного, и при поломке профилирование отключается удалением второго `--config`.
4. Профили — локальное средство разработки; аутентификации на debug-порту нет, защита — привязка к `127.0.0.1`.
5. Ретенция Pyroscope — дефолтная; данные теряются с `docker compose down -v`.

## Open Questions

Нет.

## Решённые вопросы

1. Целевые сервисы сэмплера по умолчанию — все пять, включая `notification`.
2. Связь профилей с трейсами (span profiles, кнопка «профиль спана» в Grafana) — **вне скоупа**. Штатная кнопка
   есть только у датасорса Tempo и требует обёртки `otel-profiling-go` в `platform/telemetry`; трейсы у нас в
   ClickHouse. Профили и трейсы сопоставляются вручную по `service_name` и интервалу времени.
3. Сбор профилей — через существующий OTel Collector (`pprofreceiver` → OTLP → Pyroscope), а не Grafana Alloy.
   Alloy — запасной вариант, только если spike (AC6) не прошёл, и только после согласования.
