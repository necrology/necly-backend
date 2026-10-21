-- ============================================================
-- 017: Account replacement audit for expired/complained deliveries.
--
-- A buyer may need a different account when the assigned account expires (or
-- when an admin approves a complaint). This table records every replacement:
-- the old slot ids, the new slot ids, the old and new account ids, the reason,
-- and the replacement email outcome. It never stores credentials.
--
-- Additive only: no existing row or column is changed.
-- ============================================================

BEGIN;

CREATE TABLE IF NOT EXISTS delivery_replacements (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id                 UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    order_item_id            UUID NOT NULL REFERENCES order_items(id) ON DELETE CASCADE,
    delivery_id              UUID NOT NULL REFERENCES order_deliveries(id) ON DELETE CASCADE,
    -- replacement_of points at the first old slot. old_slot_ids keeps the full
    -- set so a delivery whose quantity spans accounts is still auditable.
    replacement_of           UUID NOT NULL,
    old_inventory_account_id UUID REFERENCES inventory_accounts(id),
    inventory_account_id     UUID NOT NULL REFERENCES inventory_accounts(id),
    old_slot_ids             UUID[] NOT NULL DEFAULT '{}',
    new_slot_ids             UUID[] NOT NULL DEFAULT '{}',
    reason                   TEXT NOT NULL,
    complaint_approved       BOOLEAN NOT NULL DEFAULT FALSE,
    status                   VARCHAR(24) NOT NULL DEFAULT 'pending',
    attempts                 INTEGER NOT NULL DEFAULT 0,
    error_code               VARCHAR(64),
    email_sent_at            TIMESTAMPTZ,
    replaced_by              UUID REFERENCES users(id),
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT delivery_replacements_status_valid
        CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    CONSTRAINT delivery_replacements_attempts_non_negative CHECK (attempts >= 0)
);

CREATE INDEX IF NOT EXISTS idx_delivery_replacements_delivery_id
    ON delivery_replacements (delivery_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_delivery_replacements_order_id
    ON delivery_replacements (order_id, created_at DESC);

-- The admin detail joins replacements back to the delivery; the order index
-- keeps the per-order audit lookup bounded.
CREATE INDEX IF NOT EXISTS idx_delivery_replacements_order_item_id
    ON delivery_replacements (order_item_id);

COMMIT;
