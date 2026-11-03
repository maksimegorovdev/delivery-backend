#!/usr/bin/env bash
# Генератор трафика для практикума по Grafana: шлёт POST /api/v1/orders в gateway dev-окружения.
#
#   docs/lectures/traffic.sh                      # успешные заказы, ~5 RPS, пока не остановите Ctrl+C
#   MODE=notfound docs/lectures/traffic.sh        # несуществующий товар (404)
#   MODE=invalid docs/lectures/traffic.sh         # невалидный запрос (400)
#   PARALLEL=30 PAUSE=0 docs/lectures/traffic.sh  # 30 параллельных потоков без пауз
#   COUNT=10 docs/lectures/traffic.sh             # по 10 запросов на поток и выход
#
# Вывод: «.» — 2xx, «c» — 4xx, «E» — 5xx, «T» — таймаут или нет соединения.
set -euo pipefail

GATEWAY=${GATEWAY:-http://localhost:8080}
MODE=${MODE:-ok}
PARALLEL=${PARALLEL:-1}
PAUSE=${PAUSE:-0.2}
COUNT=${COUNT:-0}

# ID адреса и товаров генерируются при сидировании, поэтому берём их из баз dev-стека.
address=$(docker exec user-postgres psql -U postgres -d userdb -tAc "SELECT id FROM addresses LIMIT 1")
products=$(docker exec product-postgres psql -U postgres -d productdb -tAc "SELECT string_agg(id::text, ' ' ORDER BY name) FROM products")
read -r p1 p2 _ <<<"$products"

if [ -z "$address" ] || [ -z "${p1:-}" ] || [ -z "${p2:-}" ]; then
  echo "traffic: нет seed-данных (адрес или два товара); выполните task migrate-up" >&2
  exit 1
fi

body() {
  case "$MODE" in
    ok)       printf '{"address_id":"%s","items":[{"product_id":"%s","quantity":2},{"product_id":"%s","quantity":1}]}' "$address" "$p1" "$p2" ;;
    notfound) printf '{"address_id":"%s","items":[{"product_id":"%s","quantity":1}]}' "$address" "$(uuidgen | tr 'A-Z' 'a-z')" ;;
    invalid)  printf '{"address_id":"%s","items":[{"product_id":"%s","quantity":0}]}' "$address" "$p1" ;;
    *) echo "traffic: неизвестный MODE '$MODE' (ok|notfound|invalid)" >&2; exit 1 ;;
  esac
}

worker() {
  local n=0 code
  while [ "$COUNT" -eq 0 ] || [ "$n" -lt "$COUNT" ]; do
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 -X POST \
      -H 'Content-Type: application/json' -d "$(body)" "$GATEWAY/api/v1/orders") || true
    case "$code" in
      2??) printf '.' ;;
      4??) printf 'c' ;;
      5??) printf 'E' ;;
      *)   printf 'T' ;;
    esac
    n=$((n + 1))
    sleep "$PAUSE"
  done
}

echo "traffic: MODE=$MODE PARALLEL=$PARALLEL PAUSE=${PAUSE}s → $GATEWAY (Ctrl+C — стоп)"
pids=()
for _ in $(seq "$PARALLEL"); do
  worker &
  pids+=("$!")
done
# Останавливаем только свои потоки: kill 0 задел бы и вызывающий процесс, если скрипт запущен не из терминала.
trap 'kill "${pids[@]}" 2>/dev/null; echo; exit 130' INT TERM
wait
echo
