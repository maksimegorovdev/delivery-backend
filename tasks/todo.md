# Tasks: telemetry-sdk

> План: [plan.md](plan.md) · Спек: [SPEC-telemetry-sdk.md](../spec/SPEC-telemetry-sdk.md)

## Task 1: Каркас пакета, noop-путь и `task -d platform test`

**Description:** Добавить зависимости OTel из Tech Stack спека в `platform/go.mod`. Создать
`platform/telemetry` с `Config{Enabled, ServiceName, Endpoint}`, `New(ctx, Config)`, `Shutdown(ctx)`,
`LoggerProvider()`. Реализовать только `Enabled=false`: глобальные провайдеры не трогаются,
`LoggerProvider()` — noop, `Shutdown` → `nil`. При `Enabled=true` и пустом `Endpoint`/`ServiceName` —
ошибка с префиксом `telemetry:`. Добавить задачу `test` (`go test -race ./...`) в `platform/Taskfile.yml`.

**Acceptance criteria:**
- [x] `New(ctx, Config{Enabled: false})` не меняет `otel.GetTracerProvider()`/`GetMeterProvider()`/`global.GetLoggerProvider()`
- [x] `Shutdown` noop-экземпляра → `nil`; `LoggerProvider()` не `nil`
- [x] Невалидный `Config` при `Enabled=true` → ошибка `telemetry: …`

**Verification:**
- [x] `task -d platform test`
- [x] `task -d platform lint`
- [x] `go build ./...` во всех модулях go.work (риск R6)

**Dependencies:** None

**Files likely touched:**
- `platform/go.mod`, `platform/go.sum`
- `platform/telemetry/telemetry.go`, `platform/telemetry/config.go`
- `platform/telemetry/telemetry_test.go`
- `platform/Taskfile.yml`

**Estimated scope:** M

## Task 2: Resource

**Description:** `resource.go`: Resource с `service.name` = `Config.ServiceName`, `service.version` =
`vcs.revision` из `debug.ReadBuildInfo` (или `unknown`), `service.instance.id` (hostname + pid или UUID),
слияние с `resource.WithFromEnv()` (`OTEL_RESOURCE_ATTRIBUTES`) и schema URL semconv v1.47.0.

**Acceptance criteria:**
- [x] Resource содержит `service.name`, `service.version`, `service.instance.id`
- [x] Атрибуты из `OTEL_RESOURCE_ATTRIBUTES` попадают в Resource
- [x] Без build info `service.version` = `unknown`

**Verification:**
- [x] `task -d platform test` (table-driven, `t.Setenv`)

**Dependencies:** Task 1

**Files likely touched:**
- `platform/telemetry/resource.go`
- `platform/telemetry/resource_test.go`

**Estimated scope:** S

## Task 3: TracerProvider, пропагатор и error handler

**Description:** При `Enabled=true`: `otlptracegrpc` (insecure, `Endpoint`), `sdktrace.TracerProvider`
с batcher и Resource, сэмплер `ParentBased(AlwaysSample)`, если `OTEL_TRACES_SAMPLER` не задан.
Регистрация `otel.SetTracerProvider`, `otel.SetTextMapPropagator(TraceContext + Baggage)`,
`otel.SetErrorHandler` — `WARN` в `slog.Default()` не чаще раза в 30s с числом пропущенных ошибок.

**Acceptance criteria:**
- [x] После `New` глобальный TracerProvider — SDK, пропагатор содержит `traceparent` и `baggage`
- [x] При заданном `OTEL_TRACES_SAMPLER=always_off` спаны не сэмплируются
- [x] Error handler: N ошибок за интервал → один `WARN`, следующий содержит `suppressed`

**Verification:**
- [x] `task -d platform test` (handler тестируется с подменённым временем и slog в `bytes.Buffer`)
- [x] `task -d platform lint`

**Dependencies:** Task 2

**Files likely touched:**
- `platform/telemetry/telemetry.go`, `platform/telemetry/exporters.go`
- `platform/telemetry/errorhandler.go`, `platform/telemetry/errorhandler_test.go`

**Estimated scope:** M

## Task 4: MeterProvider с delta temporality

**Description:** `otlpmetricgrpc` с temporality selector: Counter/Histogram (и их Observable-варианты
Counter) → Delta, UpDownCounter/Gauge → Cumulative. `PeriodicReader` с интервалом 10s, если
`OTEL_METRIC_EXPORT_INTERVAL` не задан. `otel.SetMeterProvider`.

**Acceptance criteria:**
- [x] Selector возвращает Delta для Counter/Histogram/ObservableCounter, Cumulative для UpDownCounter/ObservableUpDownCounter/ObservableGauge
- [x] Интервал 10s по умолчанию, `OTEL_METRIC_EXPORT_INTERVAL` переопределяет
- [x] Глобальный MeterProvider — SDK после `New`

**Verification:**
- [x] `task -d platform test` (table-driven по `metric.InstrumentKind`)

**Dependencies:** Task 3

**Files likely touched:**
- `platform/telemetry/exporters.go`, `platform/telemetry/telemetry.go`
- `platform/telemetry/exporters_test.go`

**Estimated scope:** S

## Task 5: LoggerProvider, общий Shutdown и покрытие

**Description:** `otlploggrpc` + `sdklog.LoggerProvider` с batch-процессором и Resource,
`global.SetLoggerProvider`, `LoggerProvider()` отдаёт его. `Shutdown` — `errors.Join` трёх провайдеров.
Если `New` падает на середине — уже созданные провайдеры закрываются. Довести покрытие до ≥ 80%.

**Acceptance criteria:**
- [x] `LoggerProvider()` при `Enabled=true` — SDK-провайдер, зарегистрированный глобально
- [x] `Shutdown` с endpoint на закрытый порт и `context.WithTimeout(…, 2s)` возвращается ≤ deadline
- [x] `go test -cover ./telemetry/...` ≥ 80%

**Verification:**
- [x] `task -d platform test`
- [x] `cd platform && go test -cover ./telemetry/...`
- [x] `task -d platform lint`

**Dependencies:** Task 4

**Files likely touched:**
- `platform/telemetry/telemetry.go`, `platform/telemetry/exporters.go`
- `platform/telemetry/telemetry_test.go`

**Estimated scope:** S

## Checkpoint A: пакет готов
- [ ] `task -d platform test` и `task -d platform lint` зелёные, покрытие ≥ 80%
- [ ] `go build ./...` во всех модулях go.work
- [ ] Ревью API пакета с пользователем

## Task 6: Shutdown-контекст с таймаутом в `main.go` всех сервисов

**Description:** Сейчас `a.Close(ctx)` получает уже отменённый контекст `signal.NotifyContext`.
Заменить на `context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)` (константа в `main.go`)
во всех 5 сервисах. Поведение остальных closer'ов не меняется.

**Acceptance criteria:**
- [x] Во всех `services/*/cmd/main.go` `Close` вызывается с неотменённым контекстом с deadline 10s
- [ ] `Ctrl+C` завершает каждый сервис без ошибок `context canceled` в логах

**Verification:**
- [x] `task lint`
- [ ] `task watch-all` → `Ctrl+C` → все сервисы пишут `… stopped`, выход ≤ 10s

**Dependencies:** None (выполнять до Task 7)

**Files likely touched:**
- `services/{gateway,order,user,product,notification}/cmd/main.go`

**Estimated scope:** S (5 однотипных правок)

## Task 7: Пилот — order-service

**Description:** Секция `Telemetry{Enabled (OTEL_ENABLED, required), Endpoint (OTEL_EXPORTER_OTLP_ENDPOINT)}`
в `config` с валидацией ozzo: `Endpoint` обязателен при `Enabled` (`validation.When`); на `Enabled`
нет `validation.Required`, иначе `false` не пройдёт (риск R7). В `app.New` вызов `telemetry.New`
сразу после `config.New`, `cl.Add(tel.Shutdown)` первым. `.env.example` (и локальный `.env`) —
новые переменные.

**Acceptance criteria:**
- [ ] `OTEL_ENABLED=true` без endpoint → сервис не стартует с ошибкой валидации; без `OTEL_ENABLED` → ошибка `required`
- [ ] `OTEL_ENABLED=false` проходит валидацию
- [ ] С `OTEL_ENABLED=false` и `true` (`localhost:4317`) сервис стартует, gRPC-методы работают
- [ ] При `docker compose stop otel-collector` сервис работает; `Ctrl+C` ≤ 10s

**Verification:**
- [ ] `task -d services/order lint`, `go build ./...`
- [ ] `task svc-watch SVC=order` в обоих режимах, вызов метода через grpcui
- [ ] `Ctrl+C` при остановленном Collector — замер времени выхода

**Dependencies:** Task 5, Task 6

**Files likely touched:**
- `services/order/internal/config/config.go`
- `services/order/internal/app/app.go`
- `services/order/.env.example`

**Estimated scope:** S

## Task 8: user-service и product-service

**Description:** Повторить Task 7 для user и product.

**Acceptance criteria:**
- [ ] Оба сервиса стартуют с `OTEL_ENABLED=false` и `true`, `Shutdown` зарегистрирован первым
- [ ] `.env.example` содержат новые переменные

**Verification:**
- [ ] `task -d services/user lint`, `task -d services/product lint`
- [ ] Запуск обоих в двух режимах, `Ctrl+C` ≤ 10s

**Dependencies:** Task 7

**Files likely touched:**
- `services/{user,product}/internal/config/config.go`
- `services/{user,product}/internal/app/app.go`
- `services/{user,product}/.env.example`

**Estimated scope:** M

## Task 9: gateway-service и notification-service

**Description:** Повторить Task 7 для gateway (HTTP) и notification (Kafka-консьюмер).

**Acceptance criteria:**
- [ ] Оба сервиса стартуют с `OTEL_ENABLED=false` и `true`, `Shutdown` зарегистрирован первым
- [ ] notification при `Ctrl+C` корректно закрывает консьюмер, затем telemetry (порядок closer)
- [ ] `.env.example` содержат новые переменные

**Verification:**
- [ ] `task -d services/gateway lint`, `task -d services/notification lint`
- [ ] Запуск в двух режимах, запрос через Swagger gateway, `Ctrl+C` ≤ 10s

**Dependencies:** Task 7 (параллельно с Task 8)

**Files likely touched:**
- `services/{gateway,notification}/internal/config/config.go`
- `services/{gateway,notification}/internal/app/app.go`
- `services/{gateway,notification}/.env.example`

**Estimated scope:** M

## Checkpoint B: сервисы
- [ ] `task lint` по всем модулям
- [ ] `task watch-all` с `OTEL_ENABLED=true` и `false` — все 5 сервисов стартуют и завершаются ≤ 10s
- [ ] Остановка Collector не роняет ни один сервис

## Task 10: README, карта, TODO, Success Criteria спека

**Description:** Описать `platform/telemetry` в `platform/README.md` (API, env-переменные, порядок в
`app.New`). В корневом README — переменные `OTEL_*`. Карта: `telemetry-sdk` → done. TODO.md — подпункт
прогресса Observability. Отметить Success Criteria в спеке; у критерия про WARN — пометка, что он закрыт unit-тестами, а e2e
перенесён в `logs`/`tracing` (R1).

**Acceptance criteria:**
- [ ] Переменные и порядок вызова в README совпадают с кодом
- [ ] `SPEC-observability-map.md`: `telemetry-sdk` → done; Success Criteria спека отмечены

**Verification:**
- [ ] Пройти README: включить `OTEL_ENABLED=true` в одном сервисе по инструкции и запустить

**Dependencies:** Task 8, Task 9

**Files likely touched:**
- `platform/README.md`, `README.md`
- `SPEC-observability-map.md`, `SPEC-telemetry-sdk.md`, `TODO.md`

**Estimated scope:** S

## Checkpoint C: модуль завершён
- [ ] Все Success Criteria в `SPEC-telemetry-sdk.md` отмечены
- [ ] Ревью с пользователем → переход к `logs`/`tracing`/`metrics`
