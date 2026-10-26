# Spec: logs

> Модуль карты [SPEC-observability-map.md](SPEC-observability-map.md). Зависит от `telemetry-sdk`.

## Objective

Отправлять существующие `slog`-логи всех сервисов в ClickHouse (`otel.otel_logs`) через OTLP, не меняя
код, который логирует, и сохранив вывод в stdout для локальной разработки. Каждая запись, сделанная в
контексте запроса, связана с трейсом (`TraceId`/`SpanId`), чтобы в Grafana переходить из лога в трейс.

Текущее состояние: `platform/logger.New` строит `slog.TextHandler`/`JSONHandler` → `CtxHandler`
(добавляет `request_id`). Логи пишут HTTP-middleware `Logger`, gRPC-интерсептор `Logger`, `app.Run`, роутер Kafka.

### Acceptance criteria

1. `logger.New` принимает новую опцию `WithLoggerProvider(log.LoggerProvider)`. Без неё поведение не меняется.
2. С опцией каждая запись уходит в два хендлера через `slog.NewMultiHandler` (Go 1.26+):
   - **stdout** (text/json как сейчас) — с атрибутами `trace_id`, `span_id`, если в `ctx` есть валидный span;
   - **OTel** (`otelslog.NewHandler`) — `TraceId`/`SpanId` заполняются нативно из `ctx`, без дублирования в атрибутах.
3. `request_id` попадает в оба хендлера (в OTel — в `LogAttributes['request_id']`).
4. Уровень (`LOG_LEVEL`) применяется к обоим хендлерам одинаково; `SeverityText`/`SeverityNumber` в ClickHouse соответствуют уровню slog.
5. Группы `slog.GroupAttrs("http", ...)`/`("grpc", ...)` сохраняются в OTel как вложенные атрибуты
   (в `LogAttributes` — ключи с точкой, например `http.method`).
6. Атрибут `app_name`, добавляемый через `.With(...)`, остаётся (для stdout), а в ClickHouse сервис определяется по `ServiceName` из Resource.
7. Логи, записанные без `ctx` (`log.Info(...)` в `app.Run`), тоже уходят в ClickHouse — с пустым `TraceId`.

## Tech Stack

| Модуль | Версия |
|---|---|
| `go.opentelemetry.io/contrib/bridges/otelslog` | `v0.21.0` |
| `go.opentelemetry.io/otel/log` | `v0.x`, совместимая с SDK `v1.47.0` |
| `log/slog` (`slog.NewMultiHandler`) | stdlib Go 1.27 |

## Commands

```bash
task -d platform test
task -d platform lint
task watch-all && curl -s localhost:<gateway>/api/v1/orders/... # сгенерировать логи
# Grafana Explore → SQL Editor: SELECT ServiceName, SeverityText, Body, TraceId FROM otel.otel_logs ORDER BY Timestamp DESC LIMIT 20
```

## Project Structure

```
platform/logger/
  logger.go        → сборка цепочки: CtxHandler(Multi(stdoutTraceHandler, otelslog))
  options.go       → + WithLoggerProvider
  handler.go       → CtxHandler без изменений контракта (request_id)
  trace.go         → хендлер-обёртка для stdout: trace_id/span_id из ctx
  logger_test.go   → unit-тесты
services/*/internal/app/app.go → logger.WithLoggerProvider(tel.LoggerProvider())
```

## Code Style

```go
func New(opts ...Option) *slog.Logger {
	// ...
	var handler slog.Handler = newTraceHandler(base) // stdout + trace_id/span_id

	if cfg.loggerProvider != nil {
		handler = slog.NewMultiHandler(
			handler,
			otelslog.NewHandler(cfg.name, otelslog.WithLoggerProvider(cfg.loggerProvider)),
		)
	}

	return slog.New(NewCtxHandler(handler))
}
```

Соглашения: `Enabled` уровня проверяется до fan-out (OTel-хендлер не получает записи ниже `LOG_LEVEL`);
имена атрибутов — snake_case, как сейчас (`request_id`, `trace_id`, `span_id`).

## Testing Strategy

| Уровень | Что | Как |
|---|---|---|
| Unit | stdout содержит `trace_id`/`span_id` при активном span в ctx и не содержит без него | `bytes.Buffer` writer + `trace.ContextWithSpanContext` с фиксированным SpanContext |
| Unit | OTel-запись имеет TraceID/SpanID, `request_id` в атрибутах, без дублей `trace_id` | `go.opentelemetry.io/otel/log/logtest` Recorder как LoggerProvider |
| Unit | уровень ниже `LOG_LEVEL` не попадает ни в один хендлер | table-driven по уровням |
| Unit | группы `http.*` разворачиваются в атрибуты OTel | Recorder |
| Unit | без `WithLoggerProvider` вывод байт-в-байт как до изменений (кроме trace-атрибутов) | golden-строка |
| E2E | запрос через gateway → строки в `otel_logs` от gateway и order с одинаковым непустым `TraceId` | Grafana Explore → SQL Editor (требует модуль `tracing`) |

## Boundaries

- **Always:** сохранить stdout-вывод; не менять сигнатуры, которыми пользуются сервисы (`logger.New`, `logger.Err`).
- **Ask first:** смена ключей атрибутов (`request_id`, `app_name`); отключение stdout при включённом OTel; логирование тел запросов.
- **Never:** логировать PII/секреты ради «полноты»; ставить OTel-хендлер как единственный (потеря логов при недоступном Collector).

## Success Criteria

- [ ] `task -d platform test` зелёный, покрытие `platform/logger` ≥ 80%.
- [ ] В `otel.otel_logs` есть записи всех 5 сервисов (`SELECT DISTINCT ServiceName`).
- [ ] Для HTTP-запроса через gateway записи gateway и order имеют одинаковый `TraceId` (после модуля `tracing`).
- [ ] В stdout рядом с `request_id` выводятся `trace_id`/`span_id`.
- [ ] С `OTEL_ENABLED=false` логи только в stdout, без ошибок.

## Open Questions

Нет.
