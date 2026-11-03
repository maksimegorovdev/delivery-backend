# Tasks: profiling

> Plan: [plan.md](plan.md) · Spec: [spec.md](spec.md)

## Definition of Done (для каждой задачи)

- `task lint`, `task test` зелёные; для задач с изменениями `app`/`config` — и `task test-integration`.
- Покрытие затронутых пакетов не ниже порога `.testcoverage.yml`.
- Нет новых Go-зависимостей; теги образов зафиксированы.
- Boundaries спеки соблюдены: pprof не на `DefaultServeMux` и не на публичном gateway, debug-порты только на `127.0.0.1` хоста, `load/results/raw/` не коммитится, основной `deploy/otel-collector/config.yaml` не меняется.
- Doc-комментарии и комментарии в shell — на русском.
- Чекбокс задачи отмечается сразу после выполнения; коммит — только по команде пользователя.

---

## Phase 0: Spike

## Task 1: Spike — Alloy pyroscope.scrape → Pyroscope → Grafana

**Description:** Проверить на `grafana/alloy:v1.21.3` и `grafana/pyroscope:2.4.2` пункты AC6 (a)–(d): `pyroscope.scrape` опрашивает pprof на хосте через `host.docker.internal`; в Pyroscope у сервиса раздельные типы CPU, память (heap), горутины, mutex, block; `external_labels = { env = ... }` даёт label `env`, а heap не плодит label `bytes`; в Grafana (датасорс Pyroscope, Explore и Profiles Drilldown) видны `service_name`, типы, flame graph и diff. Источник профилей — временный Go-процесс с `net/http/pprof` на хосте. Результат записать в спеку. Предыдущая попытка (Collector + `pprofreceiver`) отклонена, её итог уже в спеке.

**Acceptance criteria:**
- [x] (a)–(d) подтверждены командами и выводом (`alloy validate`, UI/логи Alloy, запросы в API Pyroscope, Grafana Explore)
- [x] Итог (версии Alloy и Pyroscope, точные аргументы `pyroscope.scrape`) записан в спеку (Tech Stack / AC6)
- [x] При провале любого пункта — работа по AC6 остановлена, вопрос вынесен пользователю

**Verification:**
- [x] `docker run --rm … grafana/alloy:v1.21.3 validate <конфиг>` (или `fmt --test`)
- [x] API Pyroscope: `LabelValues(service_name)`, `ProfileTypes`, `Series` с `__profile_type__` и `env`
- [ ] Ручная: в Grafana Explore (Pyroscope) виден `service_name` тестового процесса с 5 типами профилей

**Dependencies:** None

**Files likely touched:**
- `deploy/alloy/profiles.dev.alloy` (черновик)
- `spec/changes/observability/profiling/spec.md` (результат spike)

**Estimated scope:** S

### Checkpoint 0
- [ ] Go/no-go по continuous profiling принят; при no-go — согласован запасной вариант

---

## Phase 1: pprof по запросу

## Task 2: Пакет platform/debugserver

**Description:** HTTP-сервер отладочных эндпоинтов на собственном `http.ServeMux` с маршрутами как у `net/http/pprof` (`/debug/pprof/` — индекс, именованные профили, `cmdline`, `profile`, `symbol`, `trace`); хендлеры — поверх `runtime/pprof` и `runtime/trace`, без импорта `net/http/pprof` (его `init()` регистрирует профили на `DefaultServeMux`). Функциональные опции: `WithMutexProfileFraction`, `WithBlockProfileRate` (по умолчанию 0), `WithLogger`, `WithTimeouts`. Таймауты: `ReadHeaderTimeout` 5 с, `WriteTimeout` 65 с, `IdleTimeout` 60 с. Без `otelhttp`. Жизненный цикл — как у `httpserver`: `Serve(ctx, lis)` на листенере из `listener.Listen`, graceful shutdown по отмене `ctx` с таймаутом.

**Acceptance criteria:**
- [x] `/debug/pprof/` → 200 со списком профилей; `goroutine?debug=1`, `heap`, `profile?seconds=1` отдают данные; `seconds` > `WriteTimeout` → ошибка обработчика
- [x] Неизвестный путь и пути вне `/debug/pprof/` → 404; второй сервер на `http.DefaultServeMux` в том же процессе не отдаёт профили
- [x] Сервер останавливается по отмене ctx; `goleak` чистый; покрытие не ниже порога

**Verification:**
- [x] `cd platform && go test -race ./debugserver/...`
- [x] `task lint`, `task cover`

**Dependencies:** None

**Files likely touched:**
- `platform/debugserver/server.go`
- `platform/debugserver/options.go`
- `platform/debugserver/server_test.go`
- `platform/README.md`

**Estimated scope:** M

## Task 3: debugserver в order

**Description:** Эталонная интеграция. Секция `Debug{Addr, MutexFraction, BlockRate}` в конфиге (`PPROF_ADDR` необязательный, `PPROF_MUTEX_FRACTION`/`PPROF_BLOCK_RATE` `>= 0`, по умолчанию 0). В `App.New` — `listener.Listen` и `debugserver.New` только при непустом адресе; в `App.Run` — `Serve(ctx, lis)` в `errgroup` с логом адреса. `.env.example`: `PPROF_ADDR=127.0.0.1:6061`.

**Acceptance criteria:**
- [x] Пустой `PPROF_ADDR` → сервер не создаётся, порт не слушается; отрицательные rate/fraction → ошибка валидации
- [x] При заданном адресе `curl 127.0.0.1:6061/debug/pprof/` → 200 у сервиса, запущенного через Air
- [x] Остановка сервиса гасит debug-сервер без ошибок в логе

**Verification:**
- [x] `cd services/order && go test ./internal/config/...`; `task test-integration` (app_integration_test: эндпоинт отвечает / порт не слушается)
- [x] Ручная: `task svc-watch` для order + `curl -s 127.0.0.1:6061/debug/pprof/ | head`

**Dependencies:** Task 2

**Files likely touched:**
- `services/order/internal/config/config.go`, `config_test.go`
- `services/order/internal/app/app.go`, `app_integration_test.go`
- `services/order/.env.example`

**Estimated scope:** M

## Task 4: debugserver в user и product

**Description:** То же, что Task 3, по шаблону `order`. Порты: `user` — `127.0.0.1:6062`, `product` — `127.0.0.1:6063`.

**Acceptance criteria:**
- [x] Конфиг, валидация и тесты как в Task 3 для обоих сервисов
- [x] `curl 127.0.0.1:6062/debug/pprof/` и `:6063` → 200; без `PPROF_ADDR` порты не слушаются

**Verification:**
- [x] `task test`, `task test-integration`
- [x] Ручная: `task watch-all` + `curl`

**Dependencies:** Task 3

**Files likely touched:**
- `services/{user,product}/internal/config/config.go`, `config_test.go`
- `services/{user,product}/internal/app/app.go`, `app_integration_test.go`
- `services/{user,product}/.env.example`

**Estimated scope:** M (механическое повторение)

## Task 5: debugserver в gateway и notification

**Description:** То же для `gateway` (`127.0.0.1:6060`, отдельно от публичного HTTP-сервера, pprof не на его роутере) и `notification` (`127.0.0.1:6064`, Kafka-консьюмер без HTTP-сервера).

**Acceptance criteria:**
- [x] Конфиг, валидация и тесты как в Task 3 для обоих сервисов
- [x] `curl 127.0.0.1:6060/debug/pprof/` и `:6064` → 200; на публичном порту gateway `/debug/pprof/` → 404

**Verification:**
- [x] `task test`, `task test-integration`
- [x] Ручная: `task watch-all` + `curl` на debug-порт и на порт gateway

**Dependencies:** Task 3

**Files likely touched:**
- `services/{gateway,notification}/internal/config/config.go`, `config_test.go`
- `services/{gateway,notification}/internal/app/app.go`, `app_integration_test.go`
- `services/{gateway,notification}/.env.example`

**Estimated scope:** M (механическое повторение)

## Task 6: task pprof

**Description:** Корневая задача `task pprof SERVICE=<svc> [TYPE=profile|heap|allocs|goroutine|goroutineleak|mutex|block|trace] [SECONDS=30]`: сервис → порт из таблицы AC2, снимок в `tmp/pprof/<service>-<type>-<ts>.pb.gz` (`trace` → `.trace`), печать `go tool pprof -http=:0 <файл>` (для trace — `go tool trace`).

**Acceptance criteria:**
- [x] `task pprof SERVICE=order` создаёт профиль, который открывает `go tool pprof -top`
- [x] Неизвестный `SERVICE` или `TYPE`, недоступный эндпоинт → понятная ошибка, ненулевой код, пустой файл не остаётся
- [x] `tmp/` в `.gitignore`

**Verification:**
- [x] Ручная: `task pprof SERVICE=order TYPE=heap`, `task pprof SERVICE=foo; echo $?`, `go tool pprof -top <файл>`

**Dependencies:** Task 3 (полностью — Tasks 4, 5)

**Files likely touched:**
- `Taskfile.yml`
- `.gitignore`

**Estimated scope:** S

### Checkpoint 1: pprof во всех сервисах
- [ ] `task test`, `task test-integration`, `task lint` зелёные
- [ ] `task watch-all`: `curl 127.0.0.1:6060..6064/debug/pprof/` → 200
- [ ] `task pprof` работает для каждого сервиса
- [ ] Ревью пользователя перед фазой 2

---

## Phase 2: Continuous profiling в dev

## Task 7: Pyroscope и Alloy в compose.yaml

**Description:** Сервисы `pyroscope` (тег из spike, volume, `127.0.0.1:4040`) и `alloy` (`grafana/alloy:v1.21.3`, `extra_hosts: host.docker.internal:host-gateway`, UI `127.0.0.1:12345`, `depends_on: pyroscope`, монтирование конфига read-only). `deploy/alloy/profiles.dev.alloy` в полном виде: 5 блоков `pyroscope.scrape "<svc>"` (`host.docker.internal:606x`, `service_name`, `scrape_interval = "15s"`, 5 типов в `profiling_config`) и один `pyroscope.write` → `http://pyroscope:4040` с `external_labels = { env = "dev" }`. Collector не меняется.

**Acceptance criteria:**
- [x] `task compose-up` поднимает Alloy и Pyroscope; `deploy/otel-collector/config.yaml` не изменён
- [x] Остановленный сервис даёт ошибки скрейпа в логах/UI Alloy, но остальные цели и traces/metrics/logs работают
- [x] В API Pyroscope есть `service_name` всех 5 запущенных сервисов, mutex и block — отдельные типы

**Verification:**
- [x] `docker run --rm -v …/deploy/alloy:/cfg grafana/alloy:v1.21.3 validate /cfg/profiles.dev.alloy` (или `fmt --test`)
- [x] `docker logs alloy 2>&1 | tail`
- [x] `git diff --exit-code deploy/otel-collector/config.yaml`

**Dependencies:** Task 1, Tasks 3–5

**Files likely touched:**
- `compose.yaml`
- `deploy/alloy/profiles.dev.alloy`
- `deploy/pyroscope/config.yaml` (если нужен)

**Estimated scope:** M

## Task 8: Датасорс Pyroscope в Grafana

**Description:** Provisioning `deploy/grafana/provisioning/datasources/pyroscope.yaml` (`uid: pyroscope`, URL `http://pyroscope:4040`). Проверка Explore и Profiles Drilldown.

**Acceptance criteria:**
- [x] Датасорс появляется после `task compose-up` без ручной настройки
- [x] В Profiles Drilldown видны 5 сервисов с CPU, памятью, горутинами (mutex/block — пустые в dev, это ожидаемо)
- [x] Flame graph CPU под нагрузкой (`ab` на gateway) и diff двух интервалов работают

**Verification:**
- [x] Ручная: `open http://localhost:3001/a/grafana-pyroscope-app`
- [x] `curl -s -u admin:… localhost:3001/api/datasources/uid/pyroscope` → 200

**Dependencies:** Task 7

**Files likely touched:**
- `deploy/grafana/provisioning/datasources/pyroscope.yaml`
- `compose.yaml` (`depends_on` grafana, если нужно)

**Estimated scope:** XS

### Checkpoint 2: Grafana
- [x] Profiles Drilldown: 5 сервисов, flame graph и diff работают
- [x] Traces/metrics/logs в ClickHouse не пострадали (дашборды `dashboards` открываются с данными)
- [x] Ревью пользователя перед фазой 3

---

## Phase 3: Профилирование в load-стеке

## Task 9: load/compose.yaml — debug-порты и mutex/block

**Description:** У всех пяти сервисов `PPROF_ADDR=:6060` и порт `127.0.0.1:3606x:6060` по таблице AC2; у `order`, `user`, `product` — `PPROF_MUTEX_FRACTION=5`, `PPROF_BLOCK_RATE=10000`.

**Acceptance criteria:**
- [x] После `task load-up` `curl 127.0.0.1:36060..36064/debug/pprof/` → 200
- [x] Порты опубликованы только на `127.0.0.1`
- [x] `mutex`/`block` у `order`/`user`/`product` непустые под нагрузкой

**Verification:**
- [x] `task load-up`, `curl`, `task load-smoke`, `go tool pprof -top http://127.0.0.1:36061/debug/pprof/mutex`

**Dependencies:** Tasks 3–5

**Files likely touched:**
- `load/compose.yaml`

**Estimated scope:** XS

## Task 10: load/compose.profiling.yaml, profiles.load.alloy, LOAD_PROFILING

**Description:** Override-файл: `pyroscope` и `alloy` (те же теги, что в `compose.yaml`; конфиг `profiles.load.alloy` смонтирован). `profiles.load.alloy` — как dev, но цели по именам контейнеров (`order:6060`, …) и `external_labels = { env = "load" }`. `load-up`/`load-down` и `COMPOSE` в `load/Taskfile.yml` подключают override при `LOAD_PROFILING=1`. `images-check` проверяет набор файлов, включая override.

**Acceptance criteria:**
- [ ] `task load-up` — без Pyroscope и Alloy; `LOAD_PROFILING=1 task load-up` — с ними, профили `env=load` в Pyroscope
- [ ] Неверный тег Pyroscope или Alloy в override → `images-check` падает
- [ ] `task load-down` удаляет стек в обоих режимах

**Verification:**
- [ ] `task load-up && docker compose -p delivery-load ps` (нет pyroscope и alloy); `LOAD_PROFILING=1 task load-up` (есть)
- [ ] Ручная порча тега в override → `task load-up` падает на `images-check`

**Dependencies:** Task 1, Task 7, Task 9

**Files likely touched:**
- `load/compose.profiling.yaml`
- `deploy/alloy/profiles.load.alloy`
- `load/Taskfile.yml`
- `Taskfile.yml` (`images-check`, `load-up`)

**Estimated scope:** M

## Task 11: Сэмплер pprof-snapshots.sh в load-stress

**Description:** `load/scripts/pprof-snapshots.sh <out-dir> [interval]` в стиле `docker-stats.sh`: каждые `N` с для `PPROF_TARGETS` (по умолчанию `order,user,product,gateway,notification`) снимает `profile?seconds=<N-5>`, `heap`, `goroutine`, `mutex`, `block` в `<out-dir>/<service>/<HHMMSS>-<type>.pb.gz`; ошибки — в `<out-dir>/errors.log`, сэмплер не падает. `load-stress` запускает его с периодом 30 с и гасит через `defer`.

**Acceptance criteria:**
- [ ] После `task load-stress` в `results/raw/<run>/pprof/<service>/` есть профили всех 5 типов; на каждую ступень (по `started_at`/`ended_at` из `summary.json`) — хотя бы один CPU-профиль
- [ ] Остановленный целевой сервис → строки в `errors.log`, сэмплер продолжает работу
- [ ] После завершения/прерывания `load-stress` процессов сэмплера нет

**Verification:**
- [ ] Короткий прогон: `STRESS_RATES=25,50 task load-stress`; `ls results/raw/<run>/pprof/*`; `pgrep -f pprof-snapshots` пусто
- [ ] `docker compose -p delivery-load stop user` во время прогона → `errors.log`
- [ ] `sh -n load/scripts/pprof-snapshots.sh` (и `shellcheck`, если есть)

**Dependencies:** Task 9

**Files likely touched:**
- `load/scripts/pprof-snapshots.sh`
- `load/Taskfile.yml`

**Estimated scope:** S

## Task 12: Снимки soak и task load-soak-leaks

**Description:** `load-soak` снимает `heap`, `goroutine?debug=1`, `goroutine` (pb) всех целевых сервисов в начале и в конце (`pprof/<service>/soak-{start,end}-<type>`) и запускает сэмплер с периодом 300 с. `task load-soak-leaks [RUN=<dir>]` (по умолчанию последний прогон) печатает по сервису число горутин start/end и `go tool pprof -top -diff_base=soak-start-heap soak-end-heap`.

**Acceptance criteria:**
- [ ] Снимок конца делается и при падении k6 (как `soak-snapshot`)
- [ ] `load-soak-leaks` печатает таблицу по 5 сервисам; прогон без снимков → понятная ошибка
- [ ] Сэмплер гасится через `defer`

**Verification:**
- [ ] Ручная: укороченный soak (переменная длительности профиля soak, если есть, иначе полный) + `task load-soak-leaks`

**Dependencies:** Task 11

**Files likely touched:**
- `load/Taskfile.yml`
- `Taskfile.yml` (`load-soak-leaks`)
- `load/scripts/pprof-snapshots.sh` (режим одиночного снимка, если удобнее)

**Estimated scope:** S

## Task 13: Эталонные профили и task load-pprof-diff

**Description:** Каталог `load/results/baseline/pprof/`. `task load-pprof-diff SERVICE=<svc> TYPE=<type> [RUN=<dir>]`: эталон из `baseline/pprof/`, профиль прогона по умолчанию — последний прогон, CPU-профиль на пиковой ступени (`last_good_stage` из `summary.json`); открывает `go tool pprof -http=:0 -diff_base=<эталон> <профиль>`.

**Acceptance criteria:**
- [ ] При наличии эталона открывается diff; без эталона или профиля — понятная ошибка, ненулевой код
- [ ] Выбор профиля пиковой ступени детерминирован и описан в `desc` задачи

**Verification:**
- [ ] Ручная: временно положить профиль прогона как эталон → `task load-pprof-diff SERVICE=order TYPE=profile` открывает UI

**Dependencies:** Task 11

**Files likely touched:**
- `Taskfile.yml`, `load/Taskfile.yml`
- `load/results/baseline/pprof/.gitkeep`

**Estimated scope:** S

### Checkpoint 3: load
- [ ] stress и soak кладут профили; `load-soak-leaks`, `load-pprof-diff` работают
- [ ] `task load-regression` без сэмплера и без `LOAD_PROFILING` — p95 в пределах эталона
- [ ] `task load-js-test`, `task lint` зелёные
- [ ] Ревью пользователя перед фазой 4

---

## Phase 4: Результат и документация

## Task 14: Разбор узкого места order

**Description:** Полный `task load-stress` с сэмплером. Анализ: CPU и mutex/block `order` (ожидание пула pgx, `PG_MAX_CONNS`), CPU `user`/`product` на gRPC на ступенях вокруг `last_good_stage`/`first_degraded_stage`; сравнение с `docker-stats.csv`. Вывод (гипотеза подтверждена/опровергнута) с `top`/flame graph и ссылками на профили — в `baseline.md`; используемые профили — в `baseline/pprof/`. Код горячих путей не меняется; найденные проблемы — в выводы и `TODO.md`.

**Acceptance criteria:**
- [ ] В `baseline.md` раздел с выводом по пулу pgx и CPU `user`/`product`, со ссылками на файлы в `baseline/pprof/`
- [ ] Влияние сэмплера на предел stress оценено (сравнение с текущим эталоном)
- [ ] `load-pprof-diff` работает против новых эталонных профилей

**Verification:**
- [ ] `task load-stress`; ссылки в `baseline.md` резолвятся; `go tool pprof -top` по каждому эталонному профилю

**Dependencies:** Tasks 11, 13 (желательно 10 — flame graph в Grafana)

**Files likely touched:**
- `load/results/baseline.md`
- `load/results/baseline/pprof/*`
- `TODO.md`

**Estimated scope:** M

## Task 15: map.md, README, версии в спеке

**Description:** В `map.md` — «Принятые решения»: профили в Pyroscope (нет сигнала profiles в ClickHouse-экспортёре, сигнал Alpha), сбор Grafana Alloy (`pyroscope.scrape`), а не Collector (`pprofreceiver` сливает mutex и block). README — раздел Observability: профилирование (debug-порты, `task pprof`, Grafana, load-задачи, `LOAD_PROFILING`). Теги Pyroscope и Alloy — в спеке.

**Acceptance criteria:**
- [ ] Два решения записаны в `map.md`
- [ ] README описывает все команды из раздела Commands спеки
- [ ] Все Success Criteria спеки отмечены

**Verification:**
- [ ] Ссылки в `spec/` и README резолвятся; ручная сверка команд README

**Dependencies:** Tasks 1–14

**Files likely touched:**
- `spec/specs/observability/map.md`
- `README.md`
- `spec/changes/observability/profiling/spec.md`

**Estimated scope:** S

### Checkpoint: Complete
- [ ] Все Success Criteria спеки выполнены, тесты и линтер зелёные
- [ ] Ревью пользователя
- [ ] Завершение по `spec-workflow`: статус `done`, публикация в `spec/specs/observability/profiling/`, архив `spec/changes/archive/observability/<YYYY-MM-DD>-profiling/`, `map.md` → `done`
