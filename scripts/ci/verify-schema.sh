#!/usr/bin/env bash
# CI SQL gate for the W0 baseline. Applies the production schema allow-list
# twice, verifies the W0 checksums and proves that the development seed is not
# part of the production chain.
set -euo pipefail

: "${PGHOST:?PGHOST is required}"
: "${PGUSER:?PGUSER is required}"
: "${PGDATABASE:?PGDATABASE is required}"

ROOT=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
cd "$ROOT"

echo "verifying W0 migration checksums"
(cd migrations && sha256sum -c ../deploy/validation/migration-checksums.sha256)

FILES="001_initial_schema.sql 003_localize_catalog_copy.sql 004_issue_reports.sql 005_password_reset_tokens.sql 006_product_labels.sql 007_device_count.sql 008_add_users_active.sql"

for pass in 1 2; do
  echo "schema pass $pass"
  for file in $FILES; do
    psql -v ON_ERROR_STOP=1 -q -f "migrations/$file"
  done
done

TABLES=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_type='BASE TABLE'")
[ "$TABLES" -ge 20 ] || { echo "FAIL: expected at least 20 public tables, got $TABLES"; exit 1; }

USERS=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM users")
CATEGORIES=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM categories")
PRODUCTS=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM products")
[ "$USERS" = "0" ] || { echo "FAIL: production chain inserted users"; exit 1; }
[ "$CATEGORIES" = "0" ] || { echo "FAIL: production chain inserted categories (002 seed ran)"; exit 1; }
[ "$PRODUCTS" = "0" ] || { echo "FAIL: production chain inserted products (002 seed ran)"; exit 1; }

echo "PASS: schema applied twice; tables=$TABLES; no development seed rows"
