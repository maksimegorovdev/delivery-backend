# Spec: test-foundation

> Эпик: [testing](SPEC-testing-map.md) · Статус: **approved**

## Objective

Дать общую тестовую инфраструктуру, чтобы модули `unit`, `integration`, `e2e` писали только тесты,
а не обвязку. Сейчас тесты есть в `platform` и в `config` сервисов. Нет моков, контейнеров,
единых команд и порога покрытия.

Пользователь — разработчик проекта (и AI-агент). Результат:

- одна команда запускает unit-тесты всех модулей, другая — интеграционные;
- моки генерируются одной командой из интерфейсов на стороне потребителя;
- интеграционный тест получает чистую БД с применёнными миграциями одной строкой;
- `task cover` считает покрытие и сверяет с порогами.

Работоспособность доказывает пилот в `order`: unit-тест `OrderService.CreateOrder` на моках
и integration-тест репозитория `pg` на Postgres из контейнера.

Вне скоупа: массовое написание тестов и перевод существующих (модули `unit`/`integration`),
e2e, нагрузка, CI.

## Tech Stack

| Что | Библиотека / инструмент | Где |
|---|---|---|
| Ассерты, моки | `github.com/stretchr/testify` (`require`, `assert`, `mock`) | все модули |
| Генерация моков | `github.com/vektra/mockery/v3`, шаблон `testify` | `bin/` модуля, через Taskfile |
| Утечки горутин | `go.uber.org/goleak` | пакеты с горутинами |
| Контейнеры | `github.com/testcontainers/testcontainers-go` + модули `postgres`, `redpanda` | только `platform/testkit/` |
| Миграции в тестах | `github.com/golang-migrate/migrate/v4` (библиотека, `source/file`, драйвер `pgx/v5`) | только `platform/testkit/` |
| Порог покрытия | `github.com/vladopajic/go-test-coverage/v2` | `bin/` корня, через Taskfile |

Версии фиксируются в Taskfile (`*_VERSION`) и `go.mod`, как для `golangci-lint`. Образы контейнеров
совпадают с `docker-compose.yml`: `postgres:18`, `redpandadata/redpanda:v26.2.3`.
Для интеграционных тестов нужен запущенный Docker.

## Commands

Корневой `Taskfile.yml` обходит `MODULES` , как сейчас `lint`:

```
task test               # unit: go test -race -shuffle=on -count=1 ./...
task test-integration   # integration: go test -race -shuffle=on -count=1 -tags=integration ./...
task cover              # unit + integration с -coverprofile, затем go-test-coverage --config=.testcoverage.yml
task mocks              # mockery во всех модулях с .mockery.yml
task tools              # дополнительно ставит mockery и go-test-coverage
```

В Taskfile модуля те же задачи для одного модуля: `task -d services/order test`.

## Project Structure

```
platform/testkit/                 → пакеты внутри модуля platform, импортируются только из *_test.go
  pgtest/pgtest.go                → общий контейнер Postgres на тестовый бинарь, шаблонная БД с миграциями, NewDB(t)
  kafkatest/kafkatest.go          → общий контейнер Redpanda, Brokers(t), CreateTopic(t, name)
services/<svc>/
  .mockery.yml                    → какие интерфейсы мокать
  internal/service/mocks/mocks.go → сгенерированные моки (коммитятся в git)
  internal/<pkg>/<file>_test.go   → unit-тесты рядом с кодом, без тега
  internal/repository/pg/<file>_integration_test.go → //go:build integration
.testcoverage.yml                 → пороги и исключения покрытия
```

### platform/testkit/pgtest

Изоляция — через клон шаблонной БД, а не snapshot/restore. Snapshot общий на контейнер
и не совместим с `t.Parallel`. `CREATE DATABASE ... TEMPLATE` даёт каждому тесту свою БД за десятки миллисекунд.

1. Первый вызов в тестовом бинаре (`sync.Once`) запускает `postgres:18` и применяет миграции
   из переданного каталога в БД `template_app`. Затем закрывает соединения с ней.
2. `pgtest.NewDB(t, migrationsDir) *pgxpool.Pool` создаёт `CREATE DATABASE t_<random> TEMPLATE template_app`
   и возвращает пул. В `t.Cleanup` закрывает пул и делает `DROP DATABASE`.
3. Контейнер удаляет Ryuk после завершения бинаря. Без Docker тест падает с понятной ошибкой, а не пропускается.

`testkit` лежит в модуле `platform`, потому что нужен всем сервисам. Тестовые зависимости (`testcontainers`, `golang-migrate`) попадают в `platform/go.mod`.
Пакеты `testkit` не импортируют другие пакеты `platform` и отдают только `*pgxpool.Pool`/DSN.

## Code Style

Unit-тест use case на сгенерированных моках:

```go
func TestOrderService_CreateOrder(t *testing.T) {
	t.Parallel()

	errUnavailable := errors.New("user service unavailable")

	tests := []struct {
		name    string
		setup   func(d deps)
		wantErr error
	}{
		{
			name: "propagates user service error without saving order",
			setup: func(d deps) {
				d.users.EXPECT().GetUser(mock.Anything, "user-1").Return(domain.User{}, errUnavailable)
			},
			wantErr: errUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := newDeps(t) // mocks.NewMockUserProvider(t) и т.д., AssertExpectations — автоматически в t.Cleanup
			tt.setup(d)

			_, err := d.service().CreateOrder(t.Context(), validInput())

			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
```

Integration-тест репозитория:

```go
//go:build integration

package pg_test

func TestOrderRepo_Create(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t, "../../../migrations/pg")
	repo := pg.NewOrderRepo(pool)

	err := repo.Create(t.Context(), fixtureOrder())

	require.NoError(t, err)
}
```

Конвенции:

- файл теста называется по файлу исходника (`order.go` → `order_test.go`, `order_integration_test.go`), тесты идут в порядке функций исходника;
- имена `TestType_Method`, у каждого кейса `name` на английском описывает поведение (`"returns not found when product is missing"`): имя подтеста попадает в `-run` и отчёты. Комментарии и пояснения `//nolint` остаются на русском;
- `t.Parallel()` в тесте и в каждом `t.Run`; исключения — только с `//nolint:paralleltest // причина`;
- black-box `package x_test` по умолчанию, white-box — с `//nolint:testpackage // причина` (как сейчас);
- `require` для предусловий и ошибок, `assert` — для нескольких независимых проверок; аргументы `(expected, actual)`; ошибки через `ErrorIs`/`ErrorAs`;
- `assert.New(t)`/`require.New(t)` не создаются в родителе для использования внутри `t.Run`;
- `t.Context()` вместо `context.Background()`;
- хелперы начинаются с `t.Helper()`; фикстуры — функции `fixtureX()`, а не глобальные переменные.

## Testing Strategy

Модуль сам является инфраструктурой. Его проверка:

- пилотный unit-тест `CreateOrder` (ошибка user service, отсутствующий продукт, успех с записью в tx) проходит под `task test`;
- пилотный integration-тест `pg.OrderRepo.Create` + чтение из БД проходит под `task test-integration`, два параллельных теста не видят данных друг друга;
- `task mocks` повторно не меняет файлы (`git diff --exit-code`);
- `task test` не требует Docker и не запускает интеграционные тесты.

## Изменения конфигурации

- `.golangci.yml` во всех модулях:
  - включить `thelper`;
  - `testifylint` с `enable-all`;
  - `depguard`: `testkit` и `**/mocks` запрещены в не-тестовых файлах (`files: ["!$test"]`).
- Сгенерированные моки помечены `// Code generated`, golangci-lint их пропускает.
- `.mockery.yml` (на модуль):

```yaml
template: testify
packages:
  github.com/maksimegorovdev/delivery-backend/services/order/internal/service:
    config:
      all: true
      dir: "{{.InterfaceDir}}/mocks"
      filename: mocks.go
      pkgname: mocks
      structname: "Mock{{.InterfaceName}}"
```

## Boundaries

- **Always:**
  - мокать только интерфейсы потребителя;
  - моки генерировать через `task mocks` и коммитить;
  - интеграционные тесты помечать `//go:build integration`;
  - запускать `task test` и `task lint` перед сдачей задачи.
- **Ask first:**
  - новые зависимости сверх перечисленных;
  - изменение порогов покрытия;
  - изменение `docker-compose.yml`, миграций и боевого кода (пилот не должен его менять, кроме выноса интерфейсов, если без этого не обойтись).
- **Never:**
  - `testcontainers` или `testkit` в не-тестовом коде;
  - `t.Skip` вместо исправления падающего теста;
  - моки руками на `testify/mock`;
  - подключение к локальной БД из docker compose в тестах;
  - `git commit` без команды пользователя.

## Success Criteria

- [x] `task test` зелёный во всех модулях, работает без Docker.
- [x] `task test-integration` поднимает Postgres из `testkit`, пилотный тест репозитория `order` зелёный; Redpanda-хелпер запускается smoke-тестом в самом `testkit`.
- [x] `task mocks` генерирует моки для интерфейсов `order/internal/service`, повторный запуск не даёт diff.
- [x] Пилотный unit-тест `CreateOrder` покрывает ошибку зависимости, отсутствующий продукт и успешный путь.
- [x] `task cover` печатает покрытие по пакетам и проверяет пороги из `.testcoverage.yml`. Пока модули `unit`/`integration` не готовы, пороги могут не выполняться: красный результат ожидаем и фиксируется.
- [x] `task lint` зелёный с новыми правилами (`thelper`, `testifylint`, `depguard`).
- [x] ~~Конвенции продублированы в `AGENTS.md`~~ — отменено: источник конвенций — этот спек.

## Open Questions

1. ~~Язык `name` в кейсах~~ — решено: английский; существующие русские имена переводятся в модуле `unit`.
2. ~~Коммитить ли моки~~ — решено: коммитим; устаревшие моки ловит проверка `task mocks` без diff.
