#!/bin/sh
# Start the isolated W1 validation stack. This never reads production env,
# never mounts production volumes and never creates a production secret.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$ROOT"
umask 077

if [ ! -f .runtime.env ]; then
  POSTGRES_PASSWORD=$(openssl rand -hex 24)
  REDIS_PASSWORD=$(openssl rand -hex 24)
  JWT_SECRET=$(openssl rand -hex 32)
  CREDENTIAL_ENCRYPTION_KEY=$(openssl rand -base64 32 | tr -d '\n')
  cat > .runtime.env <<EOF
POSTGRES_USER=necly_validation
POSTGRES_DB=necly_validation
POSTGRES_PASSWORD=$POSTGRES_PASSWORD
REDIS_PASSWORD=$REDIS_PASSWORD
JWT_SECRET=$JWT_SECRET
CREDENTIAL_ENCRYPTION_KEY=$CREDENTIAL_ENCRYPTION_KEY
EOF
  chmod 600 .runtime.env
  echo "generated disposable validation secrets in .runtime.env (mode 0600)"
fi
set -a
# shellcheck disable=SC1091
. ./.runtime.env
set +a
export SOURCE_SHA="${SOURCE_SHA:-$(git -C "$ROOT/../.." rev-parse HEAD)}"
export BUILD_CREATED="${BUILD_CREATED:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
export FRONTEND_CONTEXT="${FRONTEND_CONTEXT:-$ROOT/../../../necly-frontend}"
export FRONTEND_SOURCE_SHA="${FRONTEND_SOURCE_SHA:-$(git -C "$FRONTEND_CONTEXT" rev-parse HEAD)}"
export VALIDATION_SEED="${VALIDATION_SEED:-1}"

./scripts/generate-certs.sh

echo "building immutable-label validation images from source $SOURCE_SHA"
docker compose --env-file .runtime.env -f docker-compose.yml build api frontend

echo "starting disposable PostgreSQL 16 and Redis"
docker compose --env-file .runtime.env -f docker-compose.yml up -d --wait postgres redis

echo "applying schema through an explicit allow-list (002 only when seeded)"
docker compose --env-file .runtime.env -f docker-compose.yml --profile tools run --rm -e VALIDATION_SEED="$VALIDATION_SEED" migrate

echo "starting API, frontend and isolated TLS edge"
docker compose --env-file .runtime.env -f docker-compose.yml up -d --no-build --wait api frontend edge

echo "running smoke checks"
./scripts/smoke.sh
