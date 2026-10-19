-- ============================================================
-- 014: Account delivery status after an admin approves an order.
--
-- W17 sends the purchased account credentials to the verified buyer by email
-- once an administrator moves the order to "processing". This table records the
-- per-order-item outcome so the dashboard can show failures and retry them
-- without sending a second email for an item that already succeeded.
--
-- Additive only: no existing row or column is changed. The unique constraint on
-- order_item_id is the idempotency key: one delivery row (and therefore at most
-- one successful email) per order item.
-- ============================================================

BEGIN;

CREATE TABLE IF NOT EXISTS order_deliveries (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id          UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    order_item_id     UUID NOT NULL REFERENCES order_items(id) ON DELETE CASCADE,
    status            VARCHAR(24) NOT NULL DEFAULT 'pending',
    attempts          INTEGER NOT NULL DEFAULT 0,
    error_code        VARCHAR(64),
    sent_at           TIMESTAMPTZ,
    last_attempt_at   TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT order_deliveries_order_item_unique UNIQUE (order_item_id),
    CONSTRAINT order_deliveries_status_valid
        CHECK (status IN ('pending', 'sending', 'sent', 'failed')),
    CONSTRAINT order_deliveries_attempts_non_negative CHECK (attempts >= 0)
);

CREATE INDEX IF NOT EXISTS idx_order_deliveries_order_id
    ON order_deliveries (order_id);

-- A stuck "sending" row is reclaimed by retry after a short lease. This index
-- keeps that lookup and the dashboard's failed-delivery filter bounded.
CREATE INDEX IF NOT EXISTS idx_order_deliveries_status_updated_at
    ON order_deliveries (status, updated_at);

COMMIT;
