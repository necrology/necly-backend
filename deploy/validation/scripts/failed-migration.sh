#!/usr/bin/env bash
# Prove failed-migration abort and checksum-mismatch abort against disposable
# databases. W2 owns the versioned runner; this records the required SQL-level
# behavior for the C14 runbook now.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$ROOT"
# shellcheck disable=SC1091
. ./scripts/lib.sh
load_runtime_env
mkdir -p evidence
STAMP=$(timestamp)
LOG="$ROOT/evidence/failed-migration-$STAMP.log"
exec > >(tee "$LOG") 2>&1

echo "# W1 failed-migration abort proof ($STAMP)"

echo "1) checksum mismatch must abort before DDL"
TMP_CHECK=$(mktemp)
sed 's/^1e8607b7/00000000/' migration-checksums.sha256 > "$TMP_CHECK"
if (cd ../../migrations && sha256sum -c "$TMP_CHECK" >/dev/null 2>&1); then
  rm -f "$TMP_CHECK"
  echo "FAIL: tampered checksum was accepted"
  exit 1
fi
rm -f "$TMP_CHECK"
echo "PASS: checksum mismatch rejected"

echo "2) failing SQL must roll back its transaction and leave no probe table"
compose exec -T postgres psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS necly_abort_probe WITH (FORCE);" \
  -c "CREATE DATABASE necly_abort_probe;"
set +e
compose exec -T postgres psql -U "$POSTGRES_USER" -d necly_abort_probe -v ON_ERROR_STOP=1 -f /scripts/failing-migration.sql >/tmp/w1-failing-migration.out 2>&1
RC=$?
set -e
cat /tmp/w1-failing-migration.out
if [ "$RC" -eq 0 ]; then
  echo "FAIL: failing migration returned zero"
  exit 1
fi
PROBE=$(compose exec -T postgres psql -U "$POSTGRES_USER" -d necly_abort_probe -Atc "SELECT to_regclass('public.w1_abort_probe') IS NULL" | tr -d '\r')
[ "$PROBE" = "t" ] || { echo "FAIL: failed migration left table behind"; exit 1; }
compose exec -T postgres psql -U "$POSTGRES_USER" -d postgres -c "DROP DATABASE necly_abort_probe WITH (FORCE);" >/dev/null
echo "PASS: nonzero exit and no partial schema"
echo "result=PASS"
