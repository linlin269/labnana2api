#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_FILE="$ROOT_DIR/logs/labnana2api.log"
STDOUT_LOG_FILE="$ROOT_DIR/logs/labnana2api.stdout.log"
LINES="${LINES:-100}"
TARGET="${1:-app}"

mkdir -p "$(dirname "$LOG_FILE")"
touch "$LOG_FILE" "$STDOUT_LOG_FILE"

if [[ "$TARGET" == "stdout" ]]; then
  exec tail -n "$LINES" -f "$STDOUT_LOG_FILE"
fi

exec tail -n "$LINES" -f "$LOG_FILE"
