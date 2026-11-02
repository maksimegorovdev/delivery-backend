#!/bin/sh
# Сэмплер лага консьюмер-группы: раз в INTERVAL секунд пишет TOTAL-LAG группы (rpk group describe) в CSV.
# Работает до kill; запускают и останавливают task load-smoke и load-stress. Границы ступеней сопоставляет sql/lag.sql.
# Использование: group-lag.sh <compose-project> <group> <out.csv> [interval-sec]
set -eu

project=$1
group=$2
out=$3
interval=${4:-5}

echo "ts,total_lag" >"$out"
while :; do
  id=$(docker ps -q --filter "label=com.docker.compose.project=$project" --filter "label=com.docker.compose.service=redpanda")
  if [ -n "$id" ]; then
    ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    lag=$(docker exec "$id" rpk group describe "$group" 2>/dev/null | awk '$1 == "TOTAL-LAG" {print $2}')
    if [ -n "$lag" ]; then
      echo "$ts,$lag" >>"$out"
    fi
  fi
  sleep "$interval"
done
