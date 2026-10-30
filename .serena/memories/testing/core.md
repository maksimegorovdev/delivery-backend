# Тестовая инфраструктура

Эпик `testing` (`spec/specs/testing/map.md`): test-foundation (done) -> unit (done) -> integration (в работе) -> e2e -> load. CI нет: уровни запускаются локально через Taskfile.

## platform/testkit
- `pgtest` — общий Postgres-контейнер на тестовый бинарь, `NewDB` (шаблон), `CheckMigrations(tb, dir)` (up -> down -> up, после down в `public` только `schema_migrations`). Один каталог миграций на бинарь.
- `kafkatest` — один Redpanda на бинарь; `Brokers(tb)`, `CreateTopic(tb, name)` (уникальный суффикс, одна партиция), `Produce(tb, topic, records...)` (синхронно, перезаписывает Topic). Docker недоступен -> тест падает, не пропускается.
- `grpctest` — `bufconn`-соединения и фейковые gRPC-серверы.
- Импорт только из `_test.go` (depguard).

## goleak и testcontainers
- Общий контейнер оставляет горутины testcontainers: в `TestMain` пакетов, использующих testkit, игнорировать `github.com/testcontainers/testcontainers-go.(*Reaper).connect.func1`, `net/http.(*persistConn).readLoop`, `net/http.(*persistConn).writeLoop` (образец: `platform/kafka/kafkaconsumer/main_test.go`).

## Паттерны асинхронных тестов (образец `kafkaconsumer_integration_test.go`)
- Фейковый хендлер `probe` пишет попытки под mutex; ожидание через хелпер `eventually(t, check)` поверх `require.EventuallyWithT`.
- «Ничего не пришло» проверять сентинелем: после остановки consumer-а положить в топик маркер и убедиться, что новый consumer той же группы получил только его.
- Группа = функция от уникального имени топика, чтобы тесты шли параллельно.
- Остановка: отмена ctx -> ожидание выхода `Run` -> `Close`; повторный вызов безопасен через `sync.Once`.

## Модуль integration (done)
Спека: `spec/specs/testing/integration/spec.md`, архив change: `spec/changes/archive/testing/2026-10-31-integration/`. Эталон события `order.created` — `contracts/events/order.created.json`, golden-тест в `order`, парный в `notification`; смоук-тесты `app` через шов `App.Addr()`.
