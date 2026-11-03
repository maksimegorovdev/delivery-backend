#!/bin/sh
# Сэмплер pprof: раз в INTERVAL секунд снимает CPU, heap, goroutine, mutex и block профили целевых сервисов load-стека.
# Файлы: <out-dir>/<service>/<HHMMSS>-<type>.pb.gz; HHMMSS — начало цикла по локальному времени хоста
# (started_at/ended_at в summary.json — UTC), по нему снимок сопоставляется со ступенью.
# Ошибка снимка пишется в <out-dir>/errors.log, сэмплер продолжает работу. Работает до kill; запускает и останавливает task load-stress.
# Цели — PPROF_TARGETS (по умолчанию order,user,product,gateway,notification), адрес хоста — PPROF_HOST (по умолчанию 127.0.0.1).
# Использование: pprof-snapshots.sh <out-dir> [interval-sec]
set -eu

out=$1
interval=${2:-30}
targets=${PPROF_TARGETS:-order,user,product,gateway,notification}
host=${PPROF_HOST:-127.0.0.1}

# CPU-профиль занимает interval-5 секунд: оставшиеся 5 секунд уходят на остальные профили и паузу.
if [ "$interval" -le 5 ]; then
  echo "pprof-snapshots: interval должен быть больше 5 секунд, получено $interval" >&2
  exit 2
fi
cpu_seconds=$((interval - 5))

mkdir -p "$out"
errors="$out/errors.log"

# Порт debug-сервера на хосте: те же, что опубликованы в load/compose.yaml.
port_of() {
  case $1 in
    gateway) echo 36060 ;;
    order) echo 36061 ;;
    user) echo 36062 ;;
    product) echo 36063 ;;
    notification) echo 36064 ;;
    *) return 1 ;;
  esac
}

# fetch <service> <port> <ts> <type> <path> <max-time>: частичный файл не остаётся ни при ошибке, ни при kill.
fetch() {
  file="$out/$1/$3-$4.pb.gz"
  if ! msg=$(curl -fsS --max-time "$6" -o "$file.tmp" "http://$host:$2/debug/pprof/$5" 2>&1); then
    rm -f "$file.tmp"
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $1 $4: $msg" >>"$errors"
    return 0
  fi
  mv "$file.tmp" "$file"
}

# sample <service> <ts>: CPU идёт в фоне, пока снимаются остальные профили.
sample() {
  if ! port=$(port_of "$1"); then
    echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) $1: неизвестный сервис в PPROF_TARGETS" >>"$errors"
    return 0
  fi
  mkdir -p "$out/$1"
  fetch "$1" "$port" "$2" profile "profile?seconds=$cpu_seconds" $((cpu_seconds + 10)) &
  for type in heap goroutine mutex block; do
    fetch "$1" "$port" "$2" "$type" "$type" 10
  done
  wait
}

# pkill из load-stress убивает процессы сэмплера, но не их curl: добиваем его (только запросы этого прогона по пути -o) и убираем недокачанное.
trap 'pkill -f "curl .* -o $out/" 2>/dev/null || true; rm -f "$out"/*/*.tmp; exit 0' TERM INT

while :; do
  started=$(date +%s)
  ts=$(date +%H%M%S)
  for svc in $(echo "$targets" | tr ',' ' '); do
    sample "$svc" "$ts" &
  done
  wait
  elapsed=$(($(date +%s) - started))
  if [ "$elapsed" -lt "$interval" ]; then
    # Пауза в фоне и wait: ловушка TERM срабатывает сразу, а не после окончания sleep.
    sleep $((interval - elapsed)) &
    wait $!
  fi
done
