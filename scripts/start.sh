#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_NAME="labnana2api"
BIN_DIR="$ROOT_DIR/bin"
RUN_DIR="$ROOT_DIR/run"
LOG_DIR="$ROOT_DIR/logs"
BIN_PATH="$BIN_DIR/$APP_NAME"
PID_FILE="$RUN_DIR/$APP_NAME.pid"
LOG_FILE="$LOG_DIR/$APP_NAME.log"
STDOUT_LOG_FILE="$LOG_DIR/$APP_NAME.stdout.log"
CONFIG_FILE="$ROOT_DIR/config.json"
DEFAULT_PORT="18082"

mkdir -p "$BIN_DIR" "$RUN_DIR" "$LOG_DIR"

find_running_pid() {
  pgrep -f "^$BIN_PATH$" | tail -n 1
}

read_port() {
  if [[ -f "$CONFIG_FILE" ]] && command -v jq >/dev/null 2>&1; then
    jq -r '.port // 18082' "$CONFIG_FILE"
    return
  fi
  echo "$DEFAULT_PORT"
}

ensure_config() {
  if [[ -f "$CONFIG_FILE" ]]; then
    return
  fi
  if [[ -f "$ROOT_DIR/config.example.json" ]]; then
    cp "$ROOT_DIR/config.example.json" "$CONFIG_FILE"
    echo "config.json was missing. created from config.example.json"
    return
  fi
}

ensure_config

if [[ -f "$PID_FILE" ]]; then
  pid="$(cat "$PID_FILE" 2>/dev/null || true)"
  if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
    echo "$APP_NAME is already running. pid=$pid"
    echo "log file: $LOG_FILE"
    exit 0
  fi
  rm -f "$PID_FILE"
fi

existing_pid="$(find_running_pid || true)"
if [[ "$existing_pid" =~ ^[0-9]+$ ]] && kill -0 "$existing_pid" 2>/dev/null; then
  echo "$existing_pid" > "$PID_FILE"
  echo "$APP_NAME is already running. pid=$existing_pid"
  echo "log file: $LOG_FILE"
  exit 0
fi

port="$(read_port)"
if ss -ltn 2>/dev/null | grep -q ":$port[[:space:]]"; then
  echo "port $port is already in use. update config.json before starting." >&2
  exit 1
fi

(
  cd "$ROOT_DIR"
  go build -o "$BIN_PATH" ./cmd/labnana2api
)

if command -v setsid >/dev/null 2>&1; then
  nohup setsid "$BIN_PATH" >> "$STDOUT_LOG_FILE" 2>&1 < /dev/null &
else
  nohup "$BIN_PATH" >> "$STDOUT_LOG_FILE" 2>&1 < /dev/null &
fi
pid=$!
echo "$pid" > "$PID_FILE"

sleep 2

if ! kill -0 "$pid" 2>/dev/null; then
  echo "failed to start $APP_NAME. recent log:" >&2
  tail -n 50 "$LOG_FILE" >&2 || true
  rm -f "$PID_FILE"
  exit 1
fi

echo "$pid" > "$PID_FILE"
echo "$APP_NAME started. pid=$pid"
echo "base url: http://127.0.0.1:$port"
echo "admin ui: http://127.0.0.1:$port/"
echo "telemetry: http://127.0.0.1:$port/api/telemetry"
echo "log file: $LOG_FILE"
echo "stdout log: $STDOUT_LOG_FILE"
