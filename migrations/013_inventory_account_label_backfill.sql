-- ============================================================
-- 013: Backfill legacy inventory account labels.
--
-- Migration 012 added inventory_accounts.label with DEFAULT '' for every row
-- that already existed, while the W15 API contract documents the default
-- operator-facing label as "Akun inventaris". Accounts created before 012 (and
-- any row written with an empty string) would otherwise show a blank name in
-- the dashboard.
--
-- Additive and idempotent: only empty labels are touched, no row is deleted,
-- and re-running the statement is a no-op. The column stays NOT NULL with the
-- same default so later inserts without a label keep the contract value.
-- ============================================================

BEGIN;

UPDATE inventory_accounts
SET label = 'Akun inventaris'
WHERE label = '' OR label IS NULL;

COMMIT;
