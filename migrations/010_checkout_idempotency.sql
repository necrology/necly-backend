-- 010: Owner-scoped checkout idempotency and attempt ledger.
-- Adds columns/tables and replaces only the global idempotency uniqueness.
-- No order, payment, or inventory row is deleted. Safe to run twice.
BEGIN;

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS request_fingerprint VARCHAR(64);

ALTER TABLE orders
    DROP CONSTRAINT IF EXISTS orders_idempotency_key_key;

DROP INDEX IF EXISTS idx_orders_idempotency_key;
DROP INDEX IF EXISTS uni_orders_idempotency_key;

CREATE UNIQUE INDEX IF NOT EXISTS idx_orders_user_idempotency
    ON orders (user_id, idempotency_key);

CREATE TABLE IF NOT EXISTS checkout_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    idempotency_key VARCHAR(128) NOT NULL,
    request_fingerprint VARCHAR(64) NOT NULL,
    state VARCHAR(24) NOT NULL DEFAULT 'started',
    order_id UUID REFERENCES orders(id),
    provider VARCHAR(32),
    provider_payment_id VARCHAR(255),
    checkout_url TEXT,
    error_code VARCHAR(64),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uni_checkout_attempts_user_key UNIQUE (user_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_checkout_attempts_state
    ON checkout_attempts (state, updated_at);

COMMIT;
