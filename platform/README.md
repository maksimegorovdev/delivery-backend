# Platform

Общие платформенные библиотеки для сервисов.

## Команды

Все команды описаны в `Taskfile.yml`.

Посмотреть список можно через `task --list`.


## telemetry

Инициализация OpenTelemetry: Resource, `TracerProvider`, `MeterProvider`, `LoggerProvider`,
OTLP gRPC-экспортёры в Collector, W3C-пропагатор (`traceparent` + `baggage`).

### API

```go
tel, err := telemetry.New(ctx, telemetry.Config{
    Enabled:     cfg.Telemetry.Enabled,
    ServiceName: cfg.App.Name,          // service.name
    Endpoint:    cfg.Telemetry.Endpoint, // например, localhost:4317
})
cl.Add(tel.Shutdown)       // flush и закрытие всех трёх провайдеров
tel.LoggerProvider()       // для bridge slog → OTel (модуль logs); при Enabled=false — noop
```

- `Enabled=false` — ничего не создаётся и не регистрируется глобально, `Shutdown` возвращает `nil`.
- `Enabled=true` с пустым `ServiceName`/`Endpoint` — ошибка с префиксом `telemetry:`.
- Counter и Histogram экспортируются с delta temporality, UpDownCounter и Gauge — с cumulative.
- Ошибки экспорта (например, Collector недоступен) пишутся в `slog` как `WARN` не чаще раза в 30s
  с числом пропущенных ошибок.

### Переменные окружения

В конфиге сервиса (секция `Telemetry`):

| Переменная | Описание |
|---|---|
| `OTEL_ENABLED` | обязательна; `true` включает экспорт, `false` — выключает |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | адрес OTLP gRPC Collector'а; обязателен при `OTEL_ENABLED=true` |

Стандартные переменные OTel SDK, которые читаются напрямую:

| Переменная | По умолчанию |
|---|---|
| `OTEL_RESOURCE_ATTRIBUTES` | — (добавляются к Resource) |
| `OTEL_TRACES_SAMPLER` | `parentbased_always_on` |
| `OTEL_METRIC_EXPORT_INTERVAL` | `10000` (мс) |

### Порядок в `app.New`

1. `config.New`
2. `telemetry.New` и сразу `cl.Add(tel.Shutdown)` — первым, чтобы при закрытии (порядок обратный)
   телеметрия сбрасывалась последней, после остановки остальных компонентов
3. Остальные компоненты (logger, БД, клиенты, серверы)

`Close` в `main.go` вызывается с контекстом без отмены и таймаутом 10s: при недоступном Collector
завершение занимает до этого таймаута.
