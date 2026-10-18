-- 009: Order API compatibility and category image metadata.
-- Additive only. Existing rows receive empty strings. Safe to run twice.
BEGIN;

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS shipping_address TEXT NOT NULL DEFAULT '';

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS notes TEXT NOT NULL DEFAULT '';

ALTER TABLE categories
    ADD COLUMN IF NOT EXISTS image_url TEXT NOT NULL DEFAULT '';

COMMIT;
