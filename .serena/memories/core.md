# Delivery Backend: карта проекта

Учебный монорепозиторий микросервисов доставки на Go (`go.work`, Go 1.27.0). Язык общения и комментариев — русский.

## Модули (go.work)
- `platform` — общие библиотеки: apperr, appctx, closer, grpc, http, kafka/kafkaconsumer, logger, postgres, telemetry, `testkit/*`.
- `proto` — gRPC-контракты (`buf`), сгенерированный код в `proto/gen`.
- `services/{gateway,order,user,product,notification}` — gateway публичный HTTP; остальные gRPC; notification — потребитель Kafka.
- Инфраструктура: `docker-compose.yml`, `deploy/{debezium,otel-collector,clickhouse,grafana}`. Outbox -> Debezium (`deploy/debezium/order-outbox.json`) -> Redpanda -> notification (inbox).

## Слои сервиса
`cmd/main.go`, `internal/{app,config,domain,service,repository/pg,transport/{grpc|kafka|http},client,event}`, `migrations/pg`. Моки mockery лежат в `*/mocks/mocks.go`.

## Инварианты
- `git commit` только по явной команде пользователя (правило действует и для скиллов вроде `build`).
- Спеки, планы, задачи — только по скиллу `spec-workflow`: `spec/specs` (текущее), `spec/changes` (в работе), `spec/changes/archive`. `SPEC.md` и `tasks/` в корне не создавать.
- Активных change нет: `spec/changes` содержит только `archive/`. Следующий модуль эпика testing — `e2e`. Карта эпика: `spec/specs/testing/map.md`.
- Контракты событий Kafka — `contracts/events/*.json` в корне.

## Дальше
- Версии и инструменты: `mem:tech_stack`.
- Команды task/go: `mem:suggested_commands`.
- Стиль кода, тестов и линтера: `mem:conventions`.
- Что прогнать перед сдачей задачи: `mem:task_completion`.
- Тестовая инфраструктура (testkit, goleak, пирамида): `mem:testing/core`.
- Как работать с инструментами агента (Serena, скиллы, хуки): `mem:agent_tooling`.
