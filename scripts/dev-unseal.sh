#!/usr/bin/env bash
# Dev-only: 1-of-1 seal with the single share cached on disk. The share IS the
# master key — never use this flow outside local development.
set -euo pipefail

ADDR="${JANUS_ADDR:-http://127.0.0.1:8210}"
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

  # Capture the WHOLE init output, don't pipe it straight into grep. `janus
  # init` prints the one-time initial-admin credential alongside the share, and
  # it is never shown again — piping discarded it, so `make dev-up` left you
  # with an unsealed server you could not log in to.
  init_out="$("$JANUS" init --shares 1 --threshold 1 --address "$ADDR")"

  printf '%s\n' "$init_out" | grep -oE '\b[0-9a-f]{32,}\b' | head -1 > "$SHARE_FILE"
  # Guard against a format drift in the CLI output leaving a truncated or
  # empty share file behind: a 1-of-1 share is exactly 64 hex chars.
  share="$(cat "$SHARE_FILE")"
  if [ "${#share}" -ne 64 ]; then
    rm -f "$SHARE_FILE"
    echo "failed to extract a valid share from 'janus init' output" >&2
    exit 1
  fi
  echo "dev share saved to $SHARE_FILE (dev only — this is the master key)"

  # Re-print the admin block verbatim. Shown once, by design.
  admin_block="$(printf '%s\n' "$init_out" | sed -n '/Initial admin credential/,$p')"
  if [ -n "$admin_block" ]; then
    printf '\n%s\n' "$admin_block"
  else
    echo "note: could not find the admin credential in 'janus init' output;" >&2
    echo "      re-run with a fresh database if you need it." >&2
  fi
fi

# Unseal is idempotent: if already unsealed the server just reports the state.
"$JANUS" unseal --address "$ADDR" --share "$(cat "$SHARE_FILE")"
