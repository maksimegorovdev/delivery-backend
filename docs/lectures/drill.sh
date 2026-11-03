#!/usr/bin/env bash
# «Учебная авария» для практикума по Grafana: ломает случайный компонент dev-стека и не говорит какой.
#
#   docs/lectures/drill.sh start    # сломать что-то случайное
#   docs/lectures/drill.sh answer   # показать, что было сломано
#   docs/lectures/drill.sh stop     # всё починить
#
# Ломаются только контейнеры (docker pause / stop); Go-сервисы и код не трогаются.
set -euo pipefail

state="${TMPDIR:-/tmp}/delivery-drill"

failures=(
  "pause product-postgres|БД product «зависла» (docker pause): запросы к ней висят до таймаута"
  "pause user-postgres|БД user «зависла» (docker pause)"
  "stop debezium|Остановлен Debezium: CDC не забирает outbox, события не попадают в Kafka"
  "pause redpanda|Kafka (Redpanda) «зависла»: notification не может читать"
  "pause notification-postgres|БД notification «зависла»: consumer не может сохранить уведомление"
)

case "${1:-}" in
  start)
    if [ -f "$state" ]; then
      echo "drill: авария уже идёт; drill.sh stop — починить" >&2
      exit 1
    fi
    pick=${failures[$((RANDOM % ${#failures[@]}))]}
    read -r action target <<<"${pick%%|*}"
    docker "$action" "$target" >/dev/null
    printf '%s\n' "$pick" >"$state"
    echo "drill: что-то сломано. Найдите что — только через Grafana (http://localhost:3001)."
    ;;
  answer)
    [ -f "$state" ] || { echo "drill: аварии нет"; exit 0; }
    cut -d'|' -f2 "$state"
    ;;
  stop)
    for c in product-postgres user-postgres redpanda notification-postgres; do
      docker unpause "$c" >/dev/null 2>&1 || true
    done
    docker start debezium >/dev/null 2>&1 || true
    rm -f "$state"
    echo "drill: всё восстановлено"
    ;;
  *)
    echo "usage: $0 start|answer|stop" >&2
    exit 2
    ;;
esac
