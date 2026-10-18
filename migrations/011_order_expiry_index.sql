-- 011: Bounded reservation-expiry worker lookup.
-- Additive index only; no row is changed or deleted. Safe to run twice.
BEGIN;

CREATE INDEX IF NOT EXISTS idx_orders_pending_due_at
    ON orders (status, payment_due_at);

COMMIT;
