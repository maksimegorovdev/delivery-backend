# Конвенции

## Общее
- Комментарии на русском, имена кейсов `name` в тестах на английском, строчными.
- Перед Go-работой нужны скиллы `golang-how-to` и обязательные `golang-*` из `AGENTS.md` (см. `mem:agent_tooling`).
- Линт строгий (`.golangci.yml`, golden config): golines 120, godot (комментарии с точкой), godoclint с `require-stdlib-doclink` (в комментариях ссылки вида `[sync.Once]`, `[runtime.Goexit]`), `gochecknoglobals`, `nonamedreturns`, `mnd`, `testpackage`, `paralleltest`, `thelper`, `testifylint` (enable-all), `depguard` (`platform/testkit` только из `_test.go`, `math/rand` только в тестах, `log` только в `main.go`).
- `nolint` только с конкретным линтером и объяснением (`nolintlint`): `//nolint:testpackage // причина`, `//nolint:gochecknoglobals // причина`.
- Правило скилла code-style: вызов с 4+ аргументами — по аргументу на строку; функции ≤4 параметров; комментарий-обоснование при нарушении правил.
- Ошибки: sentinel с префиксом пакета и строчным текстом, `%w` внутри.
- Именование тестов: `TestType_Method_Behavior` (как `TestConsumer_Run_*`, `TestConsumer_process_*`); файл теста назван по исходнику.

## Тесты (подробности `mem:testing/core`)
- `require` по умолчанию, `assert` для нескольких независимых проверок; `testify/suite` не используем.
- `t.Parallel` везде, кроме тестов с `t.Setenv` (там `//nolint:paralleltest // t.Setenv`).
- Внутренние тесты: `//nolint:testpackage // ...`; внешние: пакет `x_test`.
- Интеграционные: `//go:build integration`, файлы `*_integration_test.go`, контейнеры только через `platform/testkit`, фиксированные UUID своих фикстур.
- Асинхронность — только `require.EventuallyWithT`, без `time.Sleep`.
- Моки: mockery v3 с типизированным `EXPECT()`; тривиальные зависимости — ручные фейки.
- `goleak.VerifyTestMain` в `TestMain` пакетов с горутинами; `otelenv.Unset()` до него, если нужен чистый OTEL_*.

## Грабли
- Не использовать `sync.OnceFunc` в тестовых хелперах, внутри которых вызывается `require`/`t.FailNow`: `runtime.Goexit` превращается в panic и роняет весь тестовый бинарь. Использовать `sync.Once`.
- Нарушение `kafkaconsumer`: любая ошибка хендлера откатывает оффсет и блокирует партицию (poison-сообщения без skip/DLQ) — это текущее поведение, не баг тестов.
