#!/bin/sh
# Destroy only the necly-validation project and its disposable volumes.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$ROOT"
if [ -f .runtime.env ]; then
  docker compose --env-file .runtime.env -f docker-compose.yml --profile restore down -v --remove-orphans
else
  docker compose -f docker-compose.yml --profile restore down -v --remove-orphans
fi
echo "validation project removed (production project necly untouched)"
