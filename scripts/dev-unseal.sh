#!/usr/bin/env bash
# Dev-only: 1-of-1 seal with the single share cached on disk. The share IS the
# master key — never use this flow outside local development.
set -euo pipefail

ADDR="${JANUS_ADDR:-http://127.0.0.1:8200}"
SHARE_FILE=".dev/janus-share"
JANUS="${JANUS_BIN:-bin/janus}"

# Wait for the server to answer health (60s budget).
for i in $(seq 1 60); do
  if "$JANUS" seal-status --address "$ADDR" >/dev/null 2>&1; then break; fi
  [ "$i" = 60 ] && { echo "server not reachable at $ADDR" >&2; exit 1; }
  sleep 1
done

status="$("$JANUS" seal-status --address "$ADDR")"

if ! echo "$status" | grep -q "initialized: true"; then
  echo "initializing dev seal (1-of-1)..."
  mkdir -p .dev
  umask 177
  "$JANUS" init --shares 1 --threshold 1 --address "$ADDR" \
    | grep -oE '\b[0-9a-f]{32,}\b' | head -1 > "$SHARE_FILE"
  echo "dev share saved to $SHARE_FILE (dev only — this is the master key)"
fi

# Unseal is idempotent: if already unsealed the server just reports the state.
"$JANUS" unseal --address "$ADDR" --share "$(cat "$SHARE_FILE")"
