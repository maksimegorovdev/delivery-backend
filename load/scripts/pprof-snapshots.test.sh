#!/bin/sh
# Тест длительности CPU-профиля в цикле pprof-snapshots.sh: curl подменён заглушкой, которая пишет запрошенные URL в лог.
# debugserver отклоняет seconds больше WriteTimeout (65 с), поэтому при большом интервале CPU-профиль должен быть ограничен.
# Запуск: sh load/scripts/pprof-snapshots.test.sh
set -u

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail=0

mkdir -p "$tmp/bin"
cat >"$tmp/bin/curl" <<'SH'
#!/bin/sh
out=""
url=""
while [ $# -gt 0 ]; do
  case $1 in
    -o) out=$2; shift 2 ;;
    http*) url=$1; shift ;;
    *) shift ;;
  esac
done
echo "$url" >>"$CURL_LOG"
[ -n "$out" ] && : >"$out"
exit 0
SH
chmod +x "$tmp/bin/curl"

# cpu_seconds_for <interval>: seconds из запроса CPU-профиля за первый тик цикла.
cpu_seconds_for() {
  log="$tmp/curl-$1.log"
  : >"$log"
  CURL_LOG="$log" PATH="$tmp/bin:$PATH" PPROF_TARGETS=order \
    sh "$here/pprof-snapshots.sh" "$tmp/out-$1" "$1" >/dev/null 2>&1 &
  pid=$!
  i=0
  while [ "$i" -lt 50 ] && ! grep -q 'profile?seconds=' "$log"; do
    sleep 0.1
    i=$((i + 1))
  done
  pkill -P "$pid" 2>/dev/null
  kill "$pid" 2>/dev/null
  wait "$pid" 2>/dev/null
  sed -n 's/.*profile?seconds=\([0-9]*\).*/\1/p' "$log" | head -1
}

check() { # check <name> <interval> <expected-seconds>
  got=$(cpu_seconds_for "$2")
  if [ "$got" = "$3" ]; then
    echo "ok: $1"
  else
    echo "FAIL: $1 (interval=$2, ждали seconds=$3, получили '${got}')"
    fail=1
  fi
}

check "короткий интервал: interval-5" 10 5
check "интервал stress 30 с: seconds=25" 30 25
check "интервал soak 300 с: CPU ограничен 30 с" 300 30

exit "$fail"
