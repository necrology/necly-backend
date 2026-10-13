-- Align existing production databases with domain.User. The initial users
-- table predates account activation support, while current registration and
-- admin queries always read/write this column.
BEGIN;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS active BOOLEAN NOT NULL DEFAULT TRUE;

CREATE INDEX IF NOT EXISTS idx_users_active ON users(active);

COMMIT;
