# Implementation Plan: dashboards

> Спек: [SPEC-dashboards.md](../spec/SPEC-dashboards.md) · Задачи: [todo.md](todo.md)

## Overview

Grafana dashboards as code: три готовых OTel-дашборда плагина `grafana-clickhouse-datasource` 4.23.2
и свой «Delivery / Services Overview» на метриках. Всё лежит в папке «Delivery» и провиженится из
`deploy/grafana/dashboards`. Go-код не меняется. Всё делается через JSON, YAML провайдера и SQL панелей.

## Architecture Decisions

- **JSON плагина берётся из контейнера.** Источник — `/var/lib/grafana/plugins/grafana-clickhouse-datasource/dashboards/`
  в образе с 4.23.2. Версия гарантирована `GF_PLUGINS_PREINSTALL_SYNC`. Единственная правка: `${DS_GRAFANA_CLICKHOUSE_DATASOURCE}` → `clickhouse`
  и удаление `__inputs`, потому что file-provisioning не разрешает inputs. Uid дашбордов плагина не меняются: на них
  ссылаются data links внутри плагина и наш Services Overview.
- **Services Overview не дублирует Service Dashboard.** На нём RED-сводка по всем сервисам (HTTP gateway, gRPC server)
  и то, чего нет в спанах: пул pgx, Kafka, runtime. gRPC client остаётся только в Service Dashboard.
- **Переходы лог ↔ трейс — штатные.** Их даёт OTel-режим датасорса (`otelEnabled` уже в `clickhouse.yaml`). Если не заработает,
  первым делом правится конфиг датасорса, а не JSON плагина.
- **Агрегация зависит от temporality.** Delta (histogram, monotonic sum) суммируется. Cumulative и gauge берутся через
  `argMax(Value, TimeUnix)`. p95 считается по `sumForEach(BucketCounts)` + `arrayCumSum` с интерполяцией. Прототип
  проверен на текущих данных и согласуется с `quantile(0.95)` по спанам.
- **Вертикальные срезы.** Каждая задача заканчивается дашбордом, который загружается и показывает данные. Services Overview
  растёт по строкам (rows): RED → Postgres → Kafka + Runtime.

## Dependency Graph

```
dashboards.yaml (folder «Delivery»)
    │
    ├── 3 дашборда плагина (Task 1)
    │       ├── Переходы лог ↔ трейс (Task 2)
    │       └── data link из Services Overview → otel-service-dashboard (Task 3)
    │
    └── services-overview.json
            ├── RED-сводка + переменная service + data link (Task 3)
            ├── Postgres (Task 4)
            └── Kafka + Runtime (Task 5)
```

Tasks 4 и 5 не зависят друг от друга, но правят один JSON, поэтому их лучше делать последовательно.

## Task List

### Phase 1: Готовые дашборды и переходы
- [x] Task 1: Provisioning в папку «Delivery» + 3 дашборда плагина
- [x] Task 2: Переходы лог → трейс → логи штатными средствами

### Checkpoint A
- [ ] 3 дашборда в `delivery`, переходы работают, ревью с пользователем

### Phase 2: Services Overview
- [ ] Task 3: Services Overview — RED-сводка, p95 по бакетам, data link в Service Dashboard
- [ ] Task 4: Services Overview — Postgres
- [ ] Task 5: Services Overview — Kafka и Runtime

### Checkpoint B (Complete)
- [ ] Success Criteria спека закрыты, статус в карте `done`, ревью с пользователем

## Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Штатный переход трейс → логи требует настройки датасорса (имя колонки TraceId, `logs` в trace config) | Med | Task 2 в начале плана. Правка — в `clickhouse.yaml`. Если штатно не получится, возвращаюсь к пользователю с вариантами (data links в JSON) |
| Дашборды плагина ссылаются на `${datasource}` с `current` = input — после замены переменная может не выбраться | Med | Подставить `clickhouse` и в `current`, проверить, что панели работают без выбора вручную |
| Крупные бакеты `rpc.*` → p95 грубый | Low | Интерполяция внутри бакета. Критерий сверки — «в пределах бакета». Изменение бакетов в Go — вне скоупа (Ask first) |
| `sumForEach` ломается при разных `ExplicitBounds` у одной метрики | Low | Группировка по `MetricName` (+ сервис). Бакеты одинаковы в рамках метрики. Проверка `uniqExact(ExplicitBounds)` в Task 3 |
| Тяжёлые SQL на сыром `otel_metrics_*` | Low | Объём dev-данных мал. MV/агрегаты — Ask first |
| Правки из UI теряются в volume `grafana-data` | Low | README: экспорт JSON → `jq .` → коммит |

## Open Questions

Нет. Вопросы спека решены 2026-10-28 (см. «Решённые вопросы» в спеке).
