#!/bin/sh
# Diff профиля прогона с эталоном: go tool pprof -http=:0 -diff_base=<эталон> <профиль прогона>.
# Эталон: <PPROF_BASELINE_DIR>/<service>-<type>.pb.gz (по умолчанию results/baseline/pprof).
# Профиль прогона выбирается детерминированно:
#   1. пиковая ступень — last_good_stage из <run-dir>/summary.json, а если он null — последняя ступень;
#   2. время снимка — самый поздний снимок CPU (<HHMMSS>-profile.pb.gz), начавшийся на плато этой ступени
#      (от plateau_started_at до ended_at); остальные типы берутся с тем же временем.
# HHMMSS — локальное время хоста, summary.json — UTC; ступень, пересекающая полночь, не поддерживается.
# Использование: pprof-diff.sh <run-dir> <service> <type>
# PPROF_DIFF_DRY=1 печатает выбранные пути вместо запуска pprof (для pprof-diff.test.sh).
set -eu

run=${1:?"pprof-diff: не задан каталог прогона"}
svc=${2:?"pprof-diff: не задан SERVICE"}
type=${3:?"pprof-diff: не задан TYPE"}
baseline_dir=${PPROF_BASELINE_DIR:-results/baseline/pprof}

case $type in
  profile | heap | allocs | goroutine | mutex | block) ;;
  *)
    echo "pprof-diff: неизвестный TYPE '$type' (profile, heap, allocs, goroutine, mutex, block)" >&2
    exit 1
    ;;
esac

base="$baseline_dir/$svc-$type.pb.gz"
if [ ! -f "$base" ]; then
  echo "pprof-diff: нет эталона $base (эталонные профили кладёт разбор в baseline.md)" >&2
  exit 1
fi
if [ ! -f "$run/summary.json" ]; then
  echo "pprof-diff: прогон не найден или без summary.json: $run (task load-stress)" >&2
  exit 1
fi

# Границы плато пиковой ступени в эпохе UTC.
bounds=$(jq -r '
  def epoch: sub("\\.[0-9]+Z$"; "Z") | fromdateiso8601;
  (.last_good_stage) as $peak
  | (if $peak == null then .stages[-1] else (.stages[] | select(.stage == $peak)) end)
  | "\(.plateau_started_at | epoch) \(.ended_at | epoch)"' "$run/summary.json" 2>/dev/null) || bounds=
if [ -z "$bounds" ]; then
  echo "pprof-diff: в $run/summary.json нет ступеней для выбора пика" >&2
  exit 1
fi
from=${bounds% *}
to=${bounds#* }

hms() {
  if date -r 0 +%H >/dev/null 2>&1; then date -r "$1" +%H%M%S; else date -d "@$1" +%H%M%S; fi
}
from_hms=$(hms "$from")
to_hms=$(hms "$to")

ts=
for f in "$run/pprof/$svc"/*-profile.pb.gz; do
  [ -f "$f" ] || continue
  name=${f##*/}
  t=${name%%-*}
  case $t in *[!0-9]* | '') continue ;; esac # soak-*-профили и прочее не подходят
  if [ "$t" -ge "$from_hms" ] && [ "$t" -lt "$to_hms" ]; then
    ts=$t # файлы отсортированы по имени, остаётся самый поздний
  fi
done
if [ -z "$ts" ]; then
  echo "pprof-diff: в $run/pprof/$svc нет CPU-профиля на плато пиковой ступени ($from_hms-$to_hms)" >&2
  exit 1
fi

target="$run/pprof/$svc/$ts-$type.pb.gz"
if [ ! -f "$target" ]; then
  echo "pprof-diff: нет профиля $target (см. $run/pprof/errors.log)" >&2
  exit 1
fi

if [ "${PPROF_DIFF_DRY:-}" = 1 ]; then
  echo "base=$base"
  echo "target=$target"
  exit 0
fi
exec go tool pprof -http=:0 -diff_base="$base" "$target"
