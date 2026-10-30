# Стек

- Go 1.27.0 (`go.work`), модули `platform`, `proto`, 5 сервисов; зависимости на уровне модулей.
- gRPC + Protobuf через `buf` (`proto/buf.yaml`, `buf.gen.yaml`); HTTP gateway.
- Kafka-совместимый брокер Redpanda (`redpandadata/redpanda:v26.2.3`), клиент `franz-go` (`kgo`, `kotel`); Debezium CDC для outbox.
- Postgres + `golang-migrate` v4.19.1 (`migrations/pg`), сервисы order/user/product/notification имеют БД.
- OpenTelemetry (traces/metrics/logs) -> OTel Collector -> ClickHouse -> Grafana.
- Тесты: `testify` (require/assert), `mockery` v3.9.0, `goleak`, `testcontainers-go` (через `platform/testkit`).
- Инструменты по модулям ставятся в `<module>/bin` (`task tools`): golangci-lint v2.15.0, go-test-coverage v2.21.0, air v1.63.0, migrate, grpcui, mockery.
- Dev-режим: `task dev` (compose + air hot reload).
