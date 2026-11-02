# Implementation Plan: profiling

> Spec: [spec.md](spec.md) · Tasks: [todo.md](todo.md) · Map: [observability](../../../specs/observability/map.md)

## Overview

Профилирование Go-сервисов тремя способами: pprof по запросу на отдельном debug-порту (`platform/debugserver`),
автоснимки профилей в `load-stress`/`load-soak` со сравнением с эталоном, непрерывный сбор через существующий
OTel Collector (`pprofreceiver` → OTLP profiles → Grafana Pyroscope). Итог модуля — не только инструмент,
но и разбор узкого места `order` под stress, записанный в `load/results/baseline.md`.

## Что уже есть в коде (опора плана)

- `platform/http/httpserver`: функциональные опции, `Serve(ctx, lis)` с graceful shutdown по отмене `ctx`;
  листенер открывается заранее через `platform/listener.Listen(ctx, cl, addr)`, закрытие регистрируется в `closer`.
- `services/<svc>/internal/app/app.go`: `New` собирает зависимости и `closer`, `Run` — `errgroup` с серверами.
- `services/<svc>/internal/config/config.go`: `caarlos0/env` + `ozzo-validation`, у каждой секции `Validate()`.
- `load/Taskfile.yml`: фоновые сэмплеры `docker-stats.sh`/`group-lag.sh` запускаются `&` и гасятся
  `defer: pkill -f '<script> .*results/raw/<run>/'`; `RUN_ID` — `date +%Y%m%d-%H%M%S`.
- `load/results/raw/<run>/summary.json`: у каждой ступени `started_at`, `plateau_started_at`, `ended_at`,
  есть `last_good_stage`/`first_degraded_stage` — по ним снимок сопоставляется со ступенью.
- Корневой `Taskfile.yml`: `images-check` (`FILE`, `PREFIX`) сверяет образы стека с `compose.yaml`; `load-up`
  вызывает его для `load/compose.yaml`.
- Collector `otel/opentelemetry-collector-contrib:0.163.2`, запускается `--config=/etc/otelcol-contrib/config.yaml`
  в `compose.yaml` и `load/compose.yaml`.

## Dependency Graph

```
Spike AC6 (Collector pprofreceiver + profiles pipeline + Pyroscope)  ── go/no-go для фазы 2–3 (continuous)
platform/debugserver
   └── подключение в сервисах (config + app + .env.example)
          ├── task pprof (снимки по запросу)
          ├── profiles.dev.yaml + pyroscope в compose.yaml + датасорс Grafana   ← spike
          └── load/compose.yaml: PPROF_* и порты
                 ├── load/compose.profiling.yaml + profiles.load.yaml + LOAD_PROFILING  ← spike
                 └── pprof-snapshots.sh
                        ├── load-stress (сэмплер 30 с)
                        ├── load-soak (start/end + сэмплер 300 с) → load-soak-leaks
                        └── load-pprof-diff ← baseline/pprof
                               └── разбор узкого места order → baseline.md
                                      └── map.md, README, публикация
```

Spike не зависит от debugserver: для проверки достаточно процесса с pprof (например, `go tool pprof`-совместимый
эндпоинт любого Go-бинаря или временный сервис на `net/http/pprof`), поэтому он идёт первым — высокий риск, fail fast.

## Architecture Decisions

- **Spike первым** (AC6): `pprofreceiver` и сигнал profiles — Alpha. Если (a)–(d) не проходят — стоп и согласование
  Alloy; задачи 2–6 и 9–14 от исхода не зависят и могут продолжаться.
- **Жизненный цикл debugserver — как у `httpserver`**: листенер через `listener.Listen` в `App.New`
  (занятый порт — ошибка старта), `Serve(ctx, lis)` в `errgroup` в `App.Run`, graceful shutdown по отмене `ctx`
  (согласовано с пользователем, AC1 спеки обновлён).
- **Вертикальные срезы по сервисам**: сначала `order` (на нём цель разбора) — эталонная интеграция, затем остальные
  четыре сервиса по тому же шаблону, двумя задачами.
- **Профильный конфиг Collector — отдельные файлы** на окружение, второй `--config`; основной `config.yaml` не трогается.
- **Load-профилирование — override-файл**, включается `LOAD_PROFILING=1`; `images-check` расширяется до набора
  файлов (`-f load/compose.yaml -f load/compose.profiling.yaml`), чтобы тег Pyroscope сверялся с `compose.yaml`.
- **Сэмплер — POSIX sh в стиле `docker-stats.sh`**, сопоставление снимков со ступенями — по времени файла и
  `summary.json` (в момент снимка ступень ещё неизвестна, поэтому имя файла — время `HHMMSS`, без номера ступени).
- **Новых Go-зависимостей нет**: только stdlib `net/http/pprof`, `runtime`.

## Task List

### Phase 0: Spike
- [ ] Task 1: Spike — `pprofreceiver` → OTLP profiles → Pyroscope на Collector из compose

### Checkpoint 0: go/no-go continuous profiling
- [ ] Пункты (a)–(d) AC6 подтверждены (или работа по AC6 остановлена и вынесена на согласование)

### Phase 1: pprof по запросу
- [ ] Task 2: Пакет `platform/debugserver` с тестами
- [ ] Task 3: debugserver в `order` (config, app, `.env.example`, тесты)
- [ ] Task 4: debugserver в `user` и `product`
- [ ] Task 5: debugserver в `gateway` и `notification`
- [ ] Task 6: Задача `task pprof`

### Checkpoint 1: pprof во всех сервисах
- [ ] `task test`, `task test-integration`, `task lint` зелёные; 5 эндпоинтов отвечают 200; `task pprof` работает

### Phase 2: Continuous profiling в dev
- [ ] Task 7: Pyroscope + профильные pipeline'ы Collector в `compose.yaml`
- [ ] Task 8: Датасорс Pyroscope в Grafana

### Checkpoint 2: Grafana
- [ ] В Profiles Drilldown 5 сервисов × 5 типов, diff двух интервалов работает; основной конфиг Collector не изменён

### Phase 3: Профилирование в load-стеке
- [ ] Task 9: `load/compose.yaml`: debug-порты и mutex/block у сервисов
- [ ] Task 10: `load/compose.profiling.yaml` + `profiles.load.yaml` + `LOAD_PROFILING`, `images-check`
- [ ] Task 11: Сэмплер `pprof-snapshots.sh` в `load-stress`
- [ ] Task 12: Снимки soak и `task load-soak-leaks`
- [ ] Task 13: Эталонные профили и `task load-pprof-diff`

### Checkpoint 3: load
- [ ] stress/soak кладут профили; diff и leaks работают; `load-regression` без профилирования в пределах эталона

### Phase 4: Результат и документация
- [ ] Task 14: Разбор узкого места `order` в `baseline.md`
- [ ] Task 15: `map.md`, README, версии в спеке

### Checkpoint: Complete
- [ ] Все Success Criteria спеки выполнены; ревью пользователя; затем завершение по `spec-workflow`

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| `pprofreceiver` нет в contrib 0.163.2 или ключи конфига иные | High | Spike первым; сверка с README компонента нужной версии; при необходимости — подъём тега Collector во всех compose-файлах |
| Pyroscope не принимает OTLP-профили от этой версии Collector (несовпадение версии proto profiles) | High | Spike (d); подбор тега Pyroscope под версию OTLP Collector; при провале — стоп и Alloy по согласованию |
| `host.docker.internal` недоступен из контейнера Collector | Med | `extra_hosts: host-gateway`; проверка `curl` из контейнера в spike |
| CPU-профиль 25 скрейпов/15 с нагружает сервисы и искажает dev-замеры | Low | Только dev; в load по умолчанию выключено; `profile?seconds=10` |
| Сэмплер в stress искажает замеры ступеней | Med | Сравнение stress с сэмплером и эталона в Task 14; вывод в `baseline.md`; `load-regression` гоняется без сэмплера |
| mutex/block rate в load меняют латентность | Med | Значения из спеки (5, 10000 нс); регрессия p95 проверяется в Checkpoint 3 |
| `WriteTimeout` обрезает длинный `trace`/`profile` | Low | Тест на `seconds` > `WriteTimeout`; `WriteTimeout` 65 с при максимуме 60 с |
| Профили эталона раздувают репозиторий | Low | Коммитятся только профили, на которые ссылается `baseline.md` |

## Open Questions

Нет.

## Решённые вопросы

1. **API debugserver** — как у `httpserver`: `listener.Listen` в `App.New` + `Serve(ctx, lis)` с graceful shutdown
   по отмене `ctx`. AC1, Project Structure и Testing Strategy спеки поправлены.
