#!/bin/sh
set -eu

ROOT="/tmp/tomcat-sentinel-live"
rm -rf "$ROOT"
mkdir -p "$ROOT/app/bin" "$ROOT/app/logs" "$ROOT/app/run" "$ROOT/backup"

cleanup() {
  if [ -f "$ROOT/app/run/app.pid" ]; then
    PID="$(cat "$ROOT/app/run/app.pid" 2>/dev/null || true)"
    if [ -n "$PID" ]; then
      kill "$PID" 2>/dev/null || true
    fi
  fi
}
trap cleanup EXIT INT TERM

cat > "$ROOT/app/bin/start.sh" <<'SH'
#!/bin/sh
set -eu
: "${APP_BASE:?APP_BASE is required}"
(exec sleep 300) >/dev/null 2>&1 &
PID="$!"
echo "$PID" > "$APP_BASE/run/app.pid"
echo "started fake netty app pid=$PID" >> "$APP_BASE/logs/app.log"
SH
chmod +x "$ROOT/app/bin/start.sh"

cat > "$ROOT/app/bin/stop.sh" <<'SH'
#!/bin/sh
set -eu
: "${APP_BASE:?APP_BASE is required}"
if [ -f "$APP_BASE/run/app.pid" ]; then
  kill "$(cat "$APP_BASE/run/app.pid")" 2>/dev/null || true
fi
SH
chmod +x "$ROOT/app/bin/stop.sh"

cat > "$ROOT/app/logs/app.log" <<'LOG'
java.lang.OutOfMemoryError: Java heap space
LOG

cat > "$ROOT/sentinel.properties" <<EOF
app.name=live-netty
app.kind=netty
app.home=$ROOT/app
app.base=$ROOT/app
pid.file=\${app.base}/run/app.pid
log.paths=\${app.base}/logs/app.log
backup.dir=$ROOT/backup
start.command=\${app.home}/bin/start.sh
stop.command=\${app.home}/bin/stop.sh
process.command_hint=sleep
check.interval=1s
down.debounce=0s
command.timeout=5s
command.output_max_bytes=32768
startup.verify.timeout=5s
startup.verify.interval=100ms
log.scan_tail_bytes=65536
log.scan_max_files=1
restart.cooldown=0s
restart.max_attempts=3
restart.window=1m
restart.on_unknown=false
restart.when_backup_fails=true
restart.min_mem_available_mb=0
resource.profile=docker-live-smoke
resource.soft_rss_limit_mb=24
resource.compression_enabled=false
oom.patterns=java.lang.OutOfMemoryError,Java heap space
shutdown.patterns=Stopping,Shutdown
EOF

tomcat-sentinel -config "$ROOT/sentinel.properties" -once > "$ROOT/sentinel.out" 2>&1

PID="$(cat "$ROOT/app/run/app.pid")"
kill -0 "$PID"

MANIFEST="$(find "$ROOT/backup" -name manifest.json -type f | head -n 1)"
if [ -z "$MANIFEST" ]; then
  echo "manifest not found" >&2
  cat "$ROOT/sentinel.out" >&2
  exit 1
fi

grep -q '"classification": "oom"' "$MANIFEST"
grep -q 'restarted=true' "$ROOT/sentinel.out"

echo "docker live smoke ok"
echo "pid=$PID"
echo "manifest=$MANIFEST"
cat "$ROOT/sentinel.out"

