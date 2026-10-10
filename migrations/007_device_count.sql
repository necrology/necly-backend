-- ============================================================
-- 007: Device count (QA baris 36, 38, 39)
--
-- QA baris 38 asks the buyer to choose how many devices the subscription covers
-- and requires the choice to be explicit, so the number is persisted per order
-- line. Existing orders predate the picker and default to the single-device
-- contract that was in force when they were placed.
--
-- products.device_options holds the choices a product offers (e.g. {1,2,4}).
-- An empty array means "one device only". Idempotent.
-- ============================================================

ALTER TABLE order_items
    ADD COLUMN IF NOT EXISTS device_count INTEGER NOT NULL DEFAULT 1;

-- ADD COLUMN IF NOT EXISTS is a no-op when the column is already present, so
-- this guard keeps a re-run from stacking duplicate constraints.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'order_items_device_count_positive'
    ) THEN
        ALTER TABLE order_items
            ADD CONSTRAINT order_items_device_count_positive CHECK (device_count >= 1);
    END IF;
END $$;

ALTER TABLE products ADD COLUMN IF NOT EXISTS device_options INTEGER[] NOT NULL DEFAULT '{}';
