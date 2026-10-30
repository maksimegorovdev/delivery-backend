# Команды

Корень (`Taskfile.yml`) гоняет задачу по списку модулей; в модуле — `task -d <module> <task>`.

- `task lint` / `task test` / `task test-integration` / `task cover` — по всем модулям. `test` без Docker; `test-integration` и `cover` требуют Docker.
- Один модуль: `task -d platform test`, `task -d services/order test-integration`, `task -d platform lint`.
- `test` = `go test -race -shuffle=on -count=1 ./...`; `test-integration` добавляет `-tags=integration`.
- Точечно: `cd platform && go test -race -shuffle=on -count=5 -tags=integration ./kafka/kafkaconsumer/...` (проверка стабильности).
- `task -d <svc> mocks` (mockery), `task -d <svc> migrate-up|migrate-down|migrate-create`, `task migrate-up` по всем.
- `task fuzz [FUZZTIME=30s]`, `task tidy`, `task sync` (`go work sync`).
- Инфраструктура: `task compose-up`, `task compose-down`, `task connect-register` (Debezium).
- Сервис: `task -d services/<svc> run|watch`.

## Darwin
- `sed -i ''` (пустой суффикс обязателен).
- Docker для интеграционных тестов должен быть запущен (`docker info`).
