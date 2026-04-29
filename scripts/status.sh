#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_NAME="labnana2api"
PID_FILE="$ROOT_DIR/run/$APP_NAME.pid"
CONFIG_FILE="$ROOT_DIR/config.json"
BIN_PATH="$ROOT_DIR/bin/$APP_NAME"
DEFAULT_PORT="18082"

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
  echo "status: stopped"
  exit 0
fi

port="$(read_port)"
echo "$pid" > "$PID_FILE"
echo "status: running"
echo "pid: $pid"
echo "base url: http://127.0.0.1:$port"
echo "admin ui: http://127.0.0.1:$port/"
