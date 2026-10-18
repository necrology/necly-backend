#!/usr/bin/env bash
# C14 restore rehearsal: fresh logical backup -> separate disposable PostgreSQL
# volume -> row-count comparison -> API /ready and public catalog smoke.
# This script never reads a production database or production backup.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$ROOT"
# shellcheck disable=SC1091
. ./scripts/lib.sh
load_runtime_env
mkdir -p backups evidence
STAMP=$(timestamp)
DUMP="$ROOT/backups/validation-$STAMP.dump"
LOG="$ROOT/evidence/restore-$STAMP.log"
exec > >(tee "$LOG") 2>&1

TABLES="users refresh_tokens email_verification_tokens categories product_durations products product_prices vouchers inventory_accounts carts cart_items orders order_items voucher_redemptions inventory_slots payments notifications audit_logs webhook_events testimonials assets issue_reports password_reset_tokens"

echo "# W1 restore rehearsal ($STAMP)"
echo "source_database=necly-validation postgres"
echo "restore_database=necly-validation postgres-restore (separate volume)"
START=$(date +%s%N | cut -c1-13)

echo "taking fresh logical backup"
compose exec -T postgres pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc > "$DUMP"
DUMP_SHA=$(sha256sum "$DUMP" | awk '{print $1}')
DUMP_BYTES=$(wc -c < "$DUMP" | tr -d ' ')
echo "backup_path=$DUMP"
echo "backup_sha256=$DUMP_SHA"
echo "backup_bytes=$DUMP_BYTES"

echo "starting isolated restore PostgreSQL (separate volume)"
compose --profile restore up -d --wait postgres-restore
compose --profile restore exec -T postgres-restore psql -U "$POSTGRES_USER" -d postgres -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS \"$POSTGRES_DB\" WITH (FORCE);" \
  -c "CREATE DATABASE \"$POSTGRES_DB\";"
echo "restoring backup into isolated database"
compose --profile restore exec -T postgres-restore pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner --no-privileges < "$DUMP"

mismatch=0
echo "row_count_comparison:"
for table in $TABLES; do
  source=$(compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "SELECT count(*) FROM $table" | tr -d '\r')
  restored=$(compose --profile restore exec -T postgres-restore psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "SELECT count(*) FROM $table" | tr -d '\r')
  printf '  %-26s source=%-6s restored=%-6s\n' "$table" "$source" "$restored"
  [ "$source" = "$restored" ] || mismatch=1
done
[ "$mismatch" -eq 0 ] || { echo "FAIL: restored row counts differ"; exit 1; }
echo "PASS: all table counts match"

echo "starting API against restored database"
compose --profile restore up -d --no-build --wait api-restore
compose --profile restore exec -T api-restore wget -qO- http://127.0.0.1:8080/ready >/tmp/w1-restore-ready.json
grep -q '"status":"ready"' /tmp/w1-restore-ready.json
compose --profile restore exec -T api-restore wget -qO- http://127.0.0.1:8080/api/v1/public/categories >/tmp/w1-restore-categories.json
jq -e '.categories | type == "array"' /tmp/w1-restore-categories.json >/dev/null
compose --profile restore exec -T api-restore wget -qO- http://127.0.0.1:8080/api/v1/public/products >/tmp/w1-restore-products.json
jq -e '.products | type == "array"' /tmp/w1-restore-products.json >/dev/null
echo "PASS: restored database serves /ready and public catalog shapes"

DURATION=$(( $(date +%s%N | cut -c1-13) - START ))
echo "restore_duration_ms=$DURATION"
echo "result=PASS"
compose --profile restore rm -sf api-restore postgres-restore >/dev/null 2>&1 || true
docker volume rm necly-validation_restore_data >/dev/null 2>&1 || true
echo "restore services removed; backup retained at $DUMP"
