-- ============================================================
-- 020: Per-account mailbox and login email (NEC-121).
--
-- An inventory account is a subscription account that receives login OTPs by
-- email. Storing the login address separately lets the OTP lookup find the
-- right mailbox without decrypting every credential row. The optional Gmail
-- app password is encrypted by the application with CredentialCipher; this
-- migration only creates the BYTEA column and never writes a plaintext secret.
--
-- Backfilling account_email from old encrypted credentials is deliberately
-- done by the service, because SQL cannot decrypt the credential blob.
--
-- Additive and idempotent: every statement may be executed again safely, and
-- existing rows keep working with an empty account_email and the central OTP
-- mailbox fallback.
-- ============================================================

BEGIN;

ALTER TABLE inventory_accounts
    ADD COLUMN IF NOT EXISTS account_email TEXT NOT NULL DEFAULT '';

ALTER TABLE inventory_accounts
    ADD COLUMN IF NOT EXISTS mailbox_app_password_encrypted BYTEA;

ALTER TABLE inventory_accounts
    ADD COLUMN IF NOT EXISTS mailbox_imap_host TEXT NOT NULL DEFAULT 'imap.gmail.com';

CREATE UNIQUE INDEX IF NOT EXISTS idx_inventory_accounts_account_email
    ON inventory_accounts (lower(account_email)) WHERE account_email <> '';

COMMIT;
