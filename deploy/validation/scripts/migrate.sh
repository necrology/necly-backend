#!/bin/sh
# Apply the W0 baseline schema into the isolated validation database.
#
# The production Compose file mounts the whole migrations directory as initdb,
# which also runs 002_seed_data.sql on a fresh volume (W0-01). This script is
# deliberately explicit: 002 is only run when VALIDATION_SEED=1, and every
# baseline file is checksum-verified before any DDL runs.
set -eu

cd /migrations

echo "verifying baseline migration checksums"
sha256sum -c /checksums.sha256

# 002 is a development seed and is never part of the production chain.
BASELINE_FILES="001_initial_schema.sql 003_localize_catalog_copy.sql 004_issue_reports.sql 005_password_reset_tokens.sql 006_product_labels.sql 007_device_count.sql 008_add_users_active.sql 009_order_compatibility.sql 010_checkout_idempotency.sql"

for file in $BASELINE_FILES; do
  echo "applying $file"
  psql -v ON_ERROR_STOP=1 -f "$file"
done

if [ "${VALIDATION_SEED:-0}" = "1" ]; then
  echo "applying 002_seed_data.sql (explicit validation seed only)"
  psql -v ON_ERROR_STOP=1 -f 002_seed_data.sql
else
  echo "skipping 002_seed_data.sql (VALIDATION_SEED=${VALIDATION_SEED:-0})"
fi

echo "baseline schema applied"
