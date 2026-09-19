# Local Development — Taskfile + Air

Подход к запуску сервисов в разработке: инфраструктура (Postgres, Kafka и т.д.) —
через `docker-compose` в корне репозитория, сами Go-сервисы — нативно на хосте
через [Task](https://taskfile.dev) с hot-reload через [Air](https://github.com/air-verse/air).

Почему не всё в docker-compose и не через k8s-инструменты (Tilt/Skaffold) — см.
раздел "Почему так" в конце файла.

---

## 1. Установка инструментов

**Task — глобально**, один раз на машину. Task — это то, чем запускается вообще
всё остальное (включая установку остальных тулов), поэтому сам себя через
Taskfile не устанавливает — classic chicken-and-egg:

```bash
brew install go-task
# или
go install github.com/go-task/task/v3/cmd/task@latest
```

**Air — локально в `./bin` каждого сервиса**, вместе с остальными dev-тулами
(`golangci-lint`, `migrate`, `grpcui`), через таск `tools`. Версия фиксируется
в `Taskfile.yml`, поэтому у всех разработчиков одна и та же версия, а не то что
случайно стоит глобально.

> `cosmtrek/air` больше не поддерживается — актуальный форк `air-verse/air`.

---

## 2. Пример: `services/order/Taskfile.yml`

```yaml
version: '3'

dotenv: ['.env']

vars:
  LOCAL_BIN: '{{.TASKFILE_DIR}}/bin'
  GOLANGCI_LINT_VERSION: v2.13.1
  MIGRATE_VERSION: v4.19.1
  GRPCUI_VERSION: v1.5.3
  AIR_VERSION: v1.63.0
  MIGRATIONS_DIR: migrations/pg

tasks:
  tools:
    desc: Install local dev tools (golangci-lint, migrate, grpcui, air) into ./bin
    env:
      GOBIN: '{{.LOCAL_BIN}}'
    cmds:
      - curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b {{.LOCAL_BIN}} {{.GOLANGCI_LINT_VERSION}}
      - go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@{{.MIGRATE_VERSION}}
      - go install github.com/fullstorydev/grpcui/cmd/grpcui@{{.GRPCUI_VERSION}}
      - go install github.com/air-verse/air@{{.AIR_VERSION}}

  lint:
    desc: Run golangci-lint
    cmds:
      - '{{.LOCAL_BIN}}/golangci-lint run'

  lint-fix:
    desc: Run golangci-lint with autofix
    cmds:
      - '{{.LOCAL_BIN}}/golangci-lint run --fix'

  migrate-create:
    desc: Create a new migration file (prompts for a name)
    cmds:
      - |
        read -p "Enter migration name: " name; \
        [ -n "$name" ] || { echo "migration name is required"; exit 1; }; \
        {{.LOCAL_BIN}}/migrate create -ext sql -dir {{.MIGRATIONS_DIR}} -seq "$name"

  migrate-up:
    desc: Apply all up migrations
    cmds:
      - "{{.LOCAL_BIN}}/migrate -database '{{.PG_DSN}}' -path {{.MIGRATIONS_DIR}} up"

  migrate-down:
    desc: Roll back all migrations
    cmds:
      - "{{.LOCAL_BIN}}/migrate -database '{{.PG_DSN}}' -path {{.MIGRATIONS_DIR}} down"

  grpcui:
    desc: Open grpcui against the local gRPC server
    cmds:
      - "{{.LOCAL_BIN}}/grpcui -plaintext '{{.GRPC_SERVER_ADDR}}'"

  run:
    desc: Run the service without hot-reload
    cmds:
      - go run cmd/main.go

  watch:
    desc: Run the service with hot-reload via Air
    cmds:
      - '{{.LOCAL_BIN}}/air -c .air.toml'

  tidy:
    desc: Tidy go.mod/go.sum
    cmds:
      - go mod tidy
```

### `services/order/.air.toml`

```toml
root = "."
tmp_dir = "tmp"

[build]
  cmd = "go build -o ./tmp/main ./cmd/main.go"
  bin = "tmp/main"
  full_bin = "./tmp/main"
  include_ext = ["go"]
  exclude_dir = ["tmp", "bin", "migrations"]
  delay = 200
  stop_on_error = true

[log]
  time = true

[misc]
  clean_on_exit = true
```

Запуск: `cd services/order && task watch`.

---

## 3. `dotenv` vs `env` в Taskfile

- `dotenv: ['.env']` — грузит переменные из уже существующего `.env`-файла
  (секреты, DSN, адреса — то, что отличается на машину и не должно быть в git).
  Именно так подхватываются `PG_DSN` и `GRPC_SERVER_ADDR`.
- `env: { KEY: value }` — статичные значения, захардкоженные прямо в Taskfile
  и попадающие в git. Годится для констант вроде названия окружения, но не для
  секретов и не для того, что уже лежит в `.env`.

`{{.VAR}}` vs `$VAR` внутри `cmds`:
- `{{.VAR}}` — переменная шаблонизатора Task, резолвится до того, как строка
  уйдёт в шелл. Работает для *любых* переменных (из `vars`, `dotenv`, `env`,
  CLI-аргументов) и в *любом* месте Taskfile (`dir`, `sources`, `status`,
  не только `cmds`).
- `$VAR` — чистое раскрытие шеллом. Работает только внутри `cmds` и только
  если переменная реально экспортирована в окружение процесса (как это
  делает `dotenv`, но не делает `vars`).

Из-за этого различия по умолчанию используется `{{.VAR}}` везде — единообразно,
не важно, откуда пришло значение.

### Кавычки в YAML

- Значение, начинающееся с `{{`, **обязательно** в кавычках — `{` в начале
  скаляра YAML читает как flow-mapping и падает без кавычек.
- Если `{{.VAR}}` не в начале строки — кавычки не нужны.
- Если внутри команды уже нужны одинарные кавычки для шелла
  (`-database '{{.PG_DSN}}'`) — снаружи берутся двойные, чтобы не экранировать.
- Внутри блочного скаляра (`|`) правила YAML не действуют — кавычки там
  подчиняются только шеллу.

---

## 4. Почему так

- **Task — глобально, Air — локально в `./bin`.** Task — это то, чем всё
  запускается, установить его через себя же нельзя. Air, как и остальные
  dev-тулы сервиса, — обычный Go-бинарник, ставится через `go install`
  с фиксированной версией в репозитории.
- **Инфраструктура — в docker-compose, сами сервисы — нативно.** Так быстрее
  itерация (пересборка Go-бинарника без слоя Docker/volume-mount) и работает
  нормальный дебаг через delve/IDE breakpoints, чего нет при запуске сервиса
  в контейнере.
- **Полный docker-compose (сервисы тоже в контейнерах) или Tilt/Skaffold** —
  для интеграционных прогонов / staging-parity, не для повседневной
  разработки. Держать это в уме на будущее, если появится k8s.
