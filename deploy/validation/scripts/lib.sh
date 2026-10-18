#!/bin/sh
# Shared helpers for the W1 disposable validation environment.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
export SOURCE_SHA="${SOURCE_SHA:-$(git -C "$ROOT/../.." rev-parse HEAD 2>/dev/null || echo unknown)}"
ENV_FILE="$ROOT/.runtime.env"

compose() {
  docker compose --env-file "$ENV_FILE" -f "$ROOT/docker-compose.yml" "$@"
}

load_runtime_env() {
  if [ ! -f "$ENV_FILE" ]; then
    echo "missing $ENV_FILE; run scripts/up.sh first" >&2
    exit 2
  fi
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
  export PGPASSWORD="$POSTGRES_PASSWORD"
}

timestamp() {
  date -u +%Y%m%dT%H%M%SZ
}

wait_for_ready() {
  url=${1:-http://127.0.0.1:58080/ready}
  attempts=${2:-60}
  i=0
  while [ "$i" -lt "$attempts" ]; do
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    i=$((i + 1))
    sleep 0.5
  done
  echo "timed out waiting for $url" >&2
  return 1
}
