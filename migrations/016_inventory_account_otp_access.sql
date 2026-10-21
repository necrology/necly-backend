-- ============================================================
-- 016: Account OTP secrets and per-slot access windows (NEC-89 Fase 2).
--
-- inventory_accounts gains an optional TOTP secret. It is stored encrypted
-- with the same CredentialCipher as the account credentials and is never part
-- of an API response. inventory_slots gains the window a buyer actually owns:
-- activated_at records when the seat was delivered, access_expires_at records
-- when that buyer's access ends. Both are nullable because existing/unsold
-- rows have no access window yet.
--
-- Additive only: no column is changed or removed. Existing slots remain valid
-- and their account sharing_limit is untouched, even when a legacy account
-- still exceeds the new five-user business limit.
-- ============================================================

BEGIN;

ALTER TABLE inventory_accounts
    ADD COLUMN IF NOT EXISTS otp_secret_encrypted BYTEA;

ALTER TABLE inventory_slots
    ADD COLUMN IF NOT EXISTS activated_at TIMESTAMPTZ;

ALTER TABLE inventory_slots
    ADD COLUMN IF NOT EXISTS access_expires_at TIMESTAMPTZ;

-- The admin detail view joins slots to orders, and expiry reminders scan the
-- access window. order_item_id had no index before, so these keep both reads
-- bounded as the slot table grows.
CREATE INDEX IF NOT EXISTS idx_inventory_slots_order_item_id
    ON inventory_slots (order_item_id);

CREATE INDEX IF NOT EXISTS idx_inventory_slots_access_expires_at
    ON inventory_slots (access_expires_at);

COMMIT;
