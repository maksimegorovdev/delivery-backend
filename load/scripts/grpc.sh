#!/bin/sh
# Прогон ghz по gRPC-методам стека нагрузки: ступени concurrency, JSON по ступеням и сводная таблица.
# Использование: grpc.sh <compose-project> <run-dir> [method]; method — имя конфига из ghz/ без .json (по умолчанию все).
# Seed-id берутся из env: USER_ID, ADDRESS_ID, PRODUCT_IDS (через запятую, не меньше 10).
# Необязательно: GHZ_STAGES (по умолчанию "10 50 100 200"), GHZ_DURATION (по умолчанию 60s).
set -eu

project=$1
run=$2
method=${3:-}
stages=${GHZ_STAGES:-"10 50 100 200"}
duration=${GHZ_DURATION:-60s}
: "${USER_ID:?}" "${ADDRESS_ID:?}" "${PRODUCT_IDS:?}"

compose="docker compose -f compose.yaml -p $project"
out=$run/ghz
mkdir -p "$out"

if [ -n "$method" ]; then
  if [ ! -f "ghz/$method.json" ]; then
    echo "grpc: нет конфига ghz/$method.json" >&2
    exit 1
  fi
  configs="ghz/$method.json"
else
  configs=$(ls ghz/*.json)
fi

for cfg in $configs; do
  name=$(basename "$cfg" .json)
  for c in $stages; do
    # Конфиг рендерится в results/: seed-id, concurrency и путь вывода у каждой ступени свои.
    jq --arg c "$c" --arg d "$duration" --arg o "/results/${out#results/}/${name}_c$c.json" \
      --arg user "$USER_ID" --arg addr "$ADDRESS_ID" --arg products "$PRODUCT_IDS" '
      ($products | split(",")) as $p
      | .concurrency = ($c | tonumber) | .duration = $d | .format = "json" | .output = $o | .["duration-stop"] = "wait"
      | walk(if type == "string" then
          (if . == "__USER_ID__" then $user
           elif . == "__ADDRESS_ID__" then $addr
           elif test("^__PRODUCT_[0-9]+__$") then $p[(capture("^__PRODUCT_(?<n>[0-9]+)__$").n | tonumber) - 1]
           else . end)
        else . end)' "$cfg" >"$out/.${name}_c$c.config.json"
    echo "grpc: $name, concurrency $c, $duration"
    $compose run --rm -T ghz --config "/results/${out#results/}/.${name}_c$c.config.json" >/dev/null
    rm -f "$out/.${name}_c$c.config.json"
  done
done

printf '\n%-24s %5s %9s %8s %8s %8s  %s\n' method conc rps p50_ms p95_ms p99_ms 'codes (count)'
for f in $(ls "$out"/*.json | sort -V); do
  b=$(basename "$f" .json)
  jq -r --arg n "${b%_c*}" --arg c "${b##*_c}" '
    def ms(p): (.latencyDistribution // [] | map(select(.percentage == p)) | first.latency // 0) / 1e6;
    [$n, $c, (.rps | floor), (ms(50) * 100 | round / 100), (ms(95) * 100 | round / 100), (ms(99) * 100 | round / 100),
     ((.statusCodeDistribution // {}) | to_entries | map("\(.key)=\(.value)") | join(" "))]
    | @tsv' "$f" | awk -F'\t' '{printf "%-24s %5s %9s %8s %8s %8s  %s\n", $1, $2, $3, $4, $5, $6, $7}'
done | tee "$out/summary.txt"
