#!/bin/sh
# Сэмплер docker stats: раз в INTERVAL секунд пишет CPU/память/сеть/диск контейнеров compose-проекта в CSV.
# Работает до kill; запускает и останавливает его task load-stress.
# Использование: docker-stats.sh <compose-project> <out.csv> [interval-sec]
set -eu

project=$1
out=$2
interval=${3:-5}

echo "ts,container,cpu_pct,mem_usage,mem_pct,net_io,block_io" >"$out"
while :; do
  ids=$(docker ps -q --filter "label=com.docker.compose.project=$project")
  if [ -n "$ids" ]; then
    ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    # shellcheck disable=SC2086
    docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},"{{.MemUsage}}",{{.MemPerc}},"{{.NetIO}}","{{.BlockIO}}"' $ids |
      awk -v ts="$ts" '{print ts "," $0}' >>"$out"
  fi
  sleep "$interval"
done
