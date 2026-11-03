#!/bin/sh
# Тест выбора профилей в pprof-diff.sh на фикстурах (режим PPROF_DIFF_DRY=1 печатает пути вместо запуска pprof).
# Запуск: sh load/scripts/pprof-diff.test.sh
set -u

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0

# Локальное время начала плато и конца ступени 3 → HHMMSS; summary.json хранит UTC.
local_hms() {
  if date -r 0 +%H >/dev/null 2>&1; then date -r "$1" +%H%M%S; else date -d "@$1" +%H%M%S; fi
}
plateau=1800000000
end=$((plateau + 90))
p_hms=$(local_hms "$plateau")
e_hms=$(local_hms "$end")
iso() { jq -nr --argjson t "$1" '$t | todate | sub("Z$"; ".000Z")'; }

mk_run() { # mk_run <dir> <last_good_stage-json>
  mkdir -p "$1/pprof/order"
  cat >"$1/summary.json" <<JSON
{"last_good_stage": $2, "stages": [
  {"stage": 1, "started_at": "$(iso $((plateau - 300)))", "plateau_started_at": "$(iso $((plateau - 270)))", "ended_at": "$(iso $((plateau - 150)))"},
  {"stage": 2, "started_at": "$(iso $((plateau - 150)))", "plateau_started_at": "$(iso $((plateau - 120)))", "ended_at": "$(iso "$plateau")"},
  {"stage": 3, "started_at": "$(iso "$plateau")", "plateau_started_at": "$(iso $((plateau + 30)))", "ended_at": "$(iso "$end")"}]}
JSON
}

check() { # check <name> <expected-substring> <exit-code-expected> <cmd...>
  name=$1 want=$2 code=$3
  shift 3
  out=$("$@" 2>&1)
  rc=$?
  if [ "$rc" -ne "$code" ] || ! printf '%s' "$out" | grep -q -- "$want"; then
    echo "FAIL: $name (rc=$rc, ждали rc=${code} и '${want}')"
    echo "$out" | sed 's/^/  /'
    fail=1
  else
    echo "ok: $name"
  fi
}

base="$tmp/baseline"
mkdir -p "$base"
: >"$base/order-profile.pb.gz"

# Прогон: last_good_stage=3, снимок до плато (ступень 2), внутри плато и после конца ступени.
run="$tmp/run1"
mk_run "$run" 3
before=$(local_hms $((plateau - 20)))
inside1=$(local_hms $((plateau + 35)))
inside2=$(local_hms $((plateau + 65)))
after=$(local_hms $((end + 10)))
for ts in "$before" "$inside1" "$inside2" "$after"; do : >"$run/pprof/order/$ts-profile.pb.gz"; done

check "последний снимок на плато пиковой ступени" "$inside2-profile.pb.gz" 0 \
  env PPROF_DIFF_DRY=1 PPROF_BASELINE_DIR="$base" sh "$here/pprof-diff.sh" "$run" order profile
check "эталон в выводе" "order-profile.pb.gz" 0 \
  env PPROF_DIFF_DRY=1 PPROF_BASELINE_DIR="$base" sh "$here/pprof-diff.sh" "$run" order profile

# last_good_stage=null → последняя ступень.
run2="$tmp/run2"
mk_run "$run2" null
mkdir -p "$run2/pprof/order"
: >"$run2/pprof/order/$inside1-profile.pb.gz"
check "last_good_stage=null → последняя ступень" "$inside1-profile.pb.gz" 0 \
  env PPROF_DIFF_DRY=1 PPROF_BASELINE_DIR="$base" sh "$here/pprof-diff.sh" "$run2" order profile

# Другой тип берётся с тем же временем снимка.
: >"$run/pprof/order/$inside2-heap.pb.gz"
: >"$base/order-heap.pb.gz"
check "тип heap с временем CPU-снимка" "$inside2-heap.pb.gz" 0 \
  env PPROF_DIFF_DRY=1 PPROF_BASELINE_DIR="$base" sh "$here/pprof-diff.sh" "$run" order heap

# Ошибки.
check "нет эталона" "эталон" 1 \
  env PPROF_DIFF_DRY=1 PPROF_BASELINE_DIR="$base" sh "$here/pprof-diff.sh" "$run" order mutex
rm -f "$run/pprof/order/"*
check "нет профиля на пиковой ступени" "профил" 1 \
  env PPROF_DIFF_DRY=1 PPROF_BASELINE_DIR="$base" sh "$here/pprof-diff.sh" "$run" order profile
check "нет прогона" "прогон" 1 \
  env PPROF_DIFF_DRY=1 PPROF_BASELINE_DIR="$base" sh "$here/pprof-diff.sh" "$tmp/none" order profile
check "неизвестный тип" "TYPE" 1 \
  env PPROF_DIFF_DRY=1 PPROF_BASELINE_DIR="$base" sh "$here/pprof-diff.sh" "$run" order foo

exit "$fail"
