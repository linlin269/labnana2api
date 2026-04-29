#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_NAME="labnana2api"
PID_FILE="$ROOT_DIR/run/$APP_NAME.pid"
BIN_PATH="$ROOT_DIR/bin/$APP_NAME"

find_running_pid() {
  pgrep -f "^$BIN_PATH$" | tail -n 1
}

pid=""
if [[ -f "$PID_FILE" ]]; then
  pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  if [[ ! "$pid" =~ ^[0-9]+$ ]] || ! kill -0 "$pid" 2>/dev/null; then
    pid=""
  fi
fi

if [[ -z "$pid" ]]; then
  pid="$(find_running_pid || true)"
fi

if [[ ! "$pid" =~ ^[0-9]+$ ]] || ! kill -0 "$pid" 2>/dev/null; then
  echo "$APP_NAME is not running."
  rm -f "$PID_FILE"
  exit 0
fi

echo "$pid" > "$PID_FILE"
kill "$pid"

for _ in {1..10}; do
  if kill -0 "$pid" 2>/dev/null; then
    sleep 1
  else
    rm -f "$PID_FILE"
    echo "$APP_NAME stopped."
    exit 0
  fi
done

echo "process $pid did not exit after SIGTERM. stop it manually if needed." >&2
exit 1
