#!/usr/bin/env bash
# Rehearse prior-image rollback in isolation. Two immutable-labeled local API
# images are built from the same pinned source SHA, then the stack is moved
# candidate -> prior and the time to /ready is measured.
#
# This uses local image IDs because W1 has no registry push permission. The
# release procedure must use the registry manifest digest recorded by CI.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$ROOT"
# shellcheck disable=SC1091
. ./scripts/lib.sh
load_runtime_env
mkdir -p evidence
STAMP=$(timestamp)
LOG="$ROOT/evidence/rollback-$STAMP.log"
exec > >(tee "$LOG") 2>&1

BASE_SHA=$(git -C "$ROOT/../.." rev-parse HEAD)
echo "# W1 rollback rehearsal ($STAMP)"
echo "source_sha=$BASE_SHA"

echo "building prior (A) and candidate (B) API images from the same source"
docker build -q \
  --label "org.opencontainers.image.revision=$BASE_SHA" \
  --label "org.opencontainers.image.source=https://github.com/necrology/necly-backend" \
  --label "com.necly.rollback-role=prior" \
  -t necly-validation-api:rollback-prior -f "$ROOT/../../Dockerfile" "$ROOT/../.." >/dev/null
docker build -q \
  --label "org.opencontainers.image.revision=$BASE_SHA" \
  --label "org.opencontainers.image.source=https://github.com/necrology/necly-backend" \
  --label "com.necly.rollback-role=candidate" \
  -t necly-validation-api:rollback-candidate -f "$ROOT/../../Dockerfile" "$ROOT/../.." >/dev/null
PRIOR=$(docker image inspect -f '{{.Id}}' necly-validation-api:rollback-prior)
CANDIDATE=$(docker image inspect -f '{{.Id}}' necly-validation-api:rollback-candidate)
echo "prior_image_id=$PRIOR"
echo "candidate_image_id=$CANDIDATE"
[ "$PRIOR" != "$CANDIDATE" ] || { echo "FAIL: images are not distinct"; exit 1; }

switch_api() {
  image=$1
  START_MS=$(date +%s%N | cut -c1-13)
  API_IMAGE="$image" compose up -d --no-build --force-recreate --wait api >/dev/null
  i=0
  while [ "$i" -lt 60 ]; do
    if compose exec -T api wget -qO- http://127.0.0.1:8080/ready 2>/dev/null | grep -q '"status":"ready"'; then
      break
    fi
    i=$((i + 1))
    sleep 0.5
  done
  [ "$i" -lt 60 ] || { echo "FAIL: API did not become ready"; exit 1; }
  END_MS=$(date +%s%N | cut -c1-13)
  RUNNING=$(docker inspect -f '{{.Image}}' necly-validation-api-1)
  [ "$RUNNING" = "$image" ] || { echo "FAIL: running image $RUNNING != $image"; exit 1; }
  echo $((END_MS - START_MS))
}

echo "starting candidate (B)"
CANDIDATE_MS=$(switch_api "$CANDIDATE")
echo "candidate_ready_ms=$CANDIDATE_MS"

echo "rolling back to prior (A)"
ROLLBACK_MS=$(switch_api "$PRIOR")
echo "rollback_ready_ms=$ROLLBACK_MS"

READY=$(compose exec -T api wget -qO- http://127.0.0.1:8080/ready)
echo "post_rollback_ready=$READY"
echo "result=PASS"
echo "note=local image IDs used; registry digest rollback remains W11 evidence"
