#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="$ROOT_DIR/config.json"
EXAMPLE_FILE="$ROOT_DIR/config.example.json"

if [[ -f "$CONFIG_FILE" ]]; then
  echo "config.json already exists: $CONFIG_FILE"
  exit 0
fi

if [[ ! -f "$EXAMPLE_FILE" ]]; then
  echo "config.example.json not found." >&2
  exit 1
fi

cp "$EXAMPLE_FILE" "$CONFIG_FILE"
chmod 600 "$CONFIG_FILE"
echo "created $CONFIG_FILE from $EXAMPLE_FILE"
