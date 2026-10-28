# Grafana dashboards

Дашборды провиженятся из этой папки в Grafana-папку **Delivery** (`uid: delivery`) —
см. `../provisioning/dashboards/dashboards.yaml`. Открыть: http://localhost:3001/dashboards/f/delivery.

## Дашборды плагина

`otel-logs-explorer.json`, `otel-traces-explorer.json`, `otel-service-dashboard.json` — штатные OTel-дашборды
плагина `grafana-clickhouse-datasource` **4.23.2** (версия закреплена `GF_PLUGINS_PREINSTALL_SYNC` в `docker-compose.yml`).
Uid дашбордов не меняются: на них ссылаются data links внутри плагина.

Единственные правки относительно оригинала: `${DS_GRAFANA_CLICKHOUSE_DATASOURCE}` → `clickhouse`
(uid датасорса из provisioning) и удалён `__inputs` — file-provisioning inputs не разрешает.

Обновить с новой версией плагина:

```sh
P=/var/lib/grafana/plugins/grafana-clickhouse-datasource/dashboards
for f in otel-logs-explorer otel-traces-explorer otel-service-dashboard; do
  docker exec grafana cat $P/$f.json \
    | sed 's/\${DS_GRAFANA_CLICKHOUSE_DATASOURCE}/clickhouse/g' \
    | jq 'del(.__inputs)' > deploy/grafana/dashboards/$f.json
done
```

Проверка, что отличий от оригинала нет, кроме подстановки:

```sh
diff <(docker exec grafana cat $P/$f.json | jq -S 'del(.__inputs)' | sed 's/${DS_GRAFANA_CLICKHOUSE_DATASOURCE}/clickhouse/g') \
     <(jq -S . deploy/grafana/dashboards/$f.json)
```

## Правки из UI

Папка смонтирована `:ro`, а `allowUiUpdates: true` сохраняет правки только в volume `grafana-data` —
при его пересоздании они теряются. Чтобы закоммитить правку: Dashboard → Export → *Export as JSON*
(без галочки «externally») → сохранить в файл → `jq . file.json > deploy/grafana/dashboards/<name>.json` → коммит.
