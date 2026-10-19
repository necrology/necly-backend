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

FILES="001_initial_schema.sql 003_localize_catalog_copy.sql 004_issue_reports.sql 005_password_reset_tokens.sql 006_product_labels.sql 007_device_count.sql 008_add_users_active.sql 009_order_compatibility.sql 010_checkout_idempotency.sql 011_order_expiry_index.sql 012_inventory_account_sharing.sql 013_inventory_account_label_backfill.sql 014_order_deliveries.sql"

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

# Migration 009 is additive and must survive the second pass.
COLUMNS=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND ((table_name='orders' AND column_name IN ('shipping_address','notes')) OR (table_name='categories' AND column_name='image_url'))")
[ "$COLUMNS" = "3" ] || { echo "FAIL: expected 3 order/category compatibility columns, got $COLUMNS"; exit 1; }

# Migration 010 must leave owner-scoped idempotency and one attempt ledger.
ATTEMPT_TABLE=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='checkout_attempts'")
FINGERPRINT=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='orders' AND column_name='request_fingerprint'")
OWNER_INDEX=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND indexname='idx_orders_user_idempotency'")
EXPIRY_INDEX=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM pg_indexes WHERE schemaname='public' AND indexname='idx_orders_pending_due_at'")
[ "$ATTEMPT_TABLE" = "1" ] || { echo "FAIL: checkout_attempts table missing"; exit 1; }
[ "$FINGERPRINT" = "1" ] || { echo "FAIL: orders.request_fingerprint missing"; exit 1; }
[ "$OWNER_INDEX" = "1" ] || { echo "FAIL: owner-scoped idempotency index missing"; exit 1; }
[ "$EXPIRY_INDEX" = "1" ] || { echo "FAIL: reservation-expiry index missing"; exit 1; }

# Migration 012 is additive and must survive the second pass.
SHARING=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='inventory_accounts' AND column_name IN ('label','sharing_limit')")
[ "$SHARING" = "2" ] || { echo "FAIL: expected inventory account label/sharing_limit columns, got $SHARING"; exit 1; }
SHARING_CHECK=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM pg_constraint WHERE conname='inventory_accounts_sharing_limit_positive'")
[ "$SHARING_CHECK" = "1" ] || { echo "FAIL: sharing_limit check constraint missing"; exit 1; }

# Migration 013 backfills empty labels; it must survive the second pass and
# leave no account with a blank operator-facing name.
EMPTY_LABELS=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM inventory_accounts WHERE label = '' OR label IS NULL")
[ "$EMPTY_LABELS" = "0" ] || { echo "FAIL: migration 013 left empty inventory labels"; exit 1; }

# Migration 014 records per-order-item delivery status; the unique item key is
# the idempotency guarantee for W17.
DELIVERY_TABLE=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='order_deliveries'")
DELIVERY_UNIQUE=$(psql -v ON_ERROR_STOP=1 -Atc "SELECT count(*) FROM pg_constraint WHERE conname='order_deliveries_order_item_unique'")
[ "$DELIVERY_TABLE" = "1" ] || { echo "FAIL: order_deliveries table missing"; exit 1; }
[ "$DELIVERY_UNIQUE" = "1" ] || { echo "FAIL: order_deliveries order_item uniqueness missing"; exit 1; }

echo "PASS: schema applied twice; tables=$TABLES; no development seed rows; compatibility columns=$COLUMNS; checkout_attempts=$ATTEMPT_TABLE; inventory sharing columns=$SHARING; order_deliveries=$DELIVERY_TABLE"
