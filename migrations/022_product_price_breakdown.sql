-- ============================================================
-- 022: Product price breakdown (NEC-136).
--
-- A product price row stores the amount a member pays for one slot. The admin
-- also needs to record how that amount was derived: the provider/base cost,
-- the admin fee, how many members share the cost, and whether the admin takes
-- one share as well.
--
-- `amount` stays the authoritative checkout price. The new columns are the
-- transparent breakdown shown in the product form and on the product detail
-- page. Existing rows keep working because every new column has a default.
--
-- Additive and idempotent: running this migration again is safe.
-- ============================================================

BEGIN;

ALTER TABLE product_prices
    ADD COLUMN IF NOT EXISTS base_amount BIGINT NOT NULL DEFAULT 0;

ALTER TABLE product_prices
    ADD COLUMN IF NOT EXISTS admin_fee BIGINT NOT NULL DEFAULT 0;

ALTER TABLE product_prices
    ADD COLUMN IF NOT EXISTS member_slots INTEGER NOT NULL DEFAULT 5;

ALTER TABLE product_prices
    ADD COLUMN IF NOT EXISTS admin_included BOOLEAN NOT NULL DEFAULT FALSE;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'product_prices_base_amount_check'
    ) THEN
        ALTER TABLE product_prices
            ADD CONSTRAINT product_prices_base_amount_check CHECK (base_amount >= 0);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'product_prices_admin_fee_check'
    ) THEN
        ALTER TABLE product_prices
            ADD CONSTRAINT product_prices_admin_fee_check CHECK (admin_fee >= 0);
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'product_prices_member_slots_check'
    ) THEN
        ALTER TABLE product_prices
            ADD CONSTRAINT product_prices_member_slots_check CHECK (member_slots > 0);
    END IF;
END
$$;

COMMIT;
