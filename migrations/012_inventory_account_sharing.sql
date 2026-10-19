-- ============================================================
-- 012: Multiple inventory accounts per product and sharing limits.
--
-- W15 allows one product to own several inventory accounts. Each account gets
-- an operator-facing label and a sharing_limit that caps how many slot rows may
-- be provisioned against it (one slot = one sellable seat).
--
-- Additive only: no row is deleted and no existing column changes type. Legacy
-- accounts keep every slot they already have. Their sharing_limit is backfilled
-- to the number of slot rows they own, so the new cap cannot make the existing
-- provisioned capacity invalid. New accounts default to a conservative limit of
-- one seat until an administrator raises it.
-- ============================================================

BEGIN;

ALTER TABLE inventory_accounts
    ADD COLUMN IF NOT EXISTS label VARCHAR(120) NOT NULL DEFAULT '';

ALTER TABLE inventory_accounts
    ADD COLUMN IF NOT EXISTS sharing_limit INTEGER NOT NULL DEFAULT 1;

-- Preserve legacy capacity: an account that already has N slots must accept N
-- without an administrator first editing the account. GREATEST keeps the
-- default for empty accounts and never lowers a limit that was already set.
UPDATE inventory_accounts a
SET sharing_limit = GREATEST(
        a.sharing_limit,
        (SELECT COUNT(*) FROM inventory_slots s WHERE s.inventory_account_id = a.id),
        1
    )
WHERE a.sharing_limit < (
        SELECT COUNT(*) FROM inventory_slots s WHERE s.inventory_account_id = a.id
    );

-- ADD CONSTRAINT has no IF NOT EXISTS on older PostgreSQL syntax, so guard the
-- re-run explicitly. The migration runner also wraps every file in a
-- transaction and records it once, but this keeps a manual re-run harmless.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'inventory_accounts_sharing_limit_positive'
    ) THEN
        ALTER TABLE inventory_accounts
            ADD CONSTRAINT inventory_accounts_sharing_limit_positive CHECK (sharing_limit >= 1);
    END IF;
END $$;

-- The admin list filters by product and status; slot counts group by account
-- and status. Both indexes keep those reads bounded as accounts grow.
CREATE INDEX IF NOT EXISTS idx_inventory_accounts_product_status
    ON inventory_accounts (product_id, status);

CREATE INDEX IF NOT EXISTS idx_inventory_slots_account_status
    ON inventory_slots (inventory_account_id, status);

COMMIT;
