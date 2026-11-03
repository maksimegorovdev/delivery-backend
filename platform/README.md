# Platform

Общие платформенные библиотеки для сервисов.

## Команды

Все команды описаны в `Taskfile.yml`.

Посмотреть список можно через `task --list`.

## debugserver

HTTP-сервер pprof на отдельном порту: `/debug/pprof/` (индекс), именованные профили (`goroutine`, `heap`, `allocs`, `mutex`, `block`, `threadcreate`), `profile`, `trace`, `cmdline`, `symbol`.

- Хендлеры написаны поверх `runtime/pprof` и `runtime/trace`, а не импортом `net/http/pprof`: тот безусловно вешает профили на `http.DefaultServeMux`.
- Собственный `http.ServeMux`, без `otelhttp`; жизненный цикл как у `httpserver`: `Serve(ctx, lis)` на листенере из `listener.Listen`, graceful shutdown по отмене `ctx`.
- Опции: `WithMutexProfileFraction`, `WithBlockProfileRate` (по умолчанию выключены; включаются на время `Serve`), `WithLogger`, `WithTimeouts`.
- `seconds` у `profile` и `trace` не может превышать `WriteTimeout` (по умолчанию 65 с), иначе 400.
- Слушать только loopback или внутреннюю сеть: эндпоинты не аутентифицируются.
