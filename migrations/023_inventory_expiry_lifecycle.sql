-- ============================================================
-- 023: Inventory expiry lifecycle and idempotent reminders.
--
-- The daily lifecycle job needs two supporting pieces:
--
-- 1. A deterministic dedupe key on notifications, so running the job twice
--    (or running two application instances at once) cannot send the same H-7
--    or H-1 reminder twice. Existing notifications leave the column NULL;
--    PostgreSQL allows any number of NULL values in a unique index.
-- 2. An index on inventory_accounts.expires_at. The job scans accounts whose
--    provider subscription has lapsed and the reminder query scans the H-7 /
--    H-1 windows. inventory_slots already has the equivalent index from 018.
--
-- Additive and safe to re-run: no existing row or column is changed.
-- ============================================================

BEGIN;

ALTER TABLE notifications
    ADD COLUMN IF NOT EXISTS dedupe_key VARCHAR(160);

CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_dedupe_key
    ON notifications (dedupe_key);

CREATE INDEX IF NOT EXISTS idx_inventory_accounts_expires_at
    ON inventory_accounts (expires_at);

COMMIT;
