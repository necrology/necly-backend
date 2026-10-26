package migrations

import (
	"strings"
	"testing"
)

func TestInventoryAccountMailboxMigrationIsAdditiveAndSecretFree(t *testing.T) {
	sql := readMigration(t, "020_inventory_account_mailbox.sql")
	upperSQL := strings.ToUpper(sql)

	for _, required := range []string{
		"BEGIN;",
		"ALTER TABLE INVENTORY_ACCOUNTS",
		"ADD COLUMN IF NOT EXISTS ACCOUNT_EMAIL TEXT NOT NULL DEFAULT ''",
		"ADD COLUMN IF NOT EXISTS MAILBOX_APP_PASSWORD_ENCRYPTED BYTEA",
		"ADD COLUMN IF NOT EXISTS MAILBOX_IMAP_HOST TEXT NOT NULL DEFAULT 'IMAP.GMAIL.COM'",
		"CREATE UNIQUE INDEX IF NOT EXISTS IDX_INVENTORY_ACCOUNTS_ACCOUNT_EMAIL",
		"ON INVENTORY_ACCOUNTS (LOWER(ACCOUNT_EMAIL)) WHERE ACCOUNT_EMAIL <> ''",
		"COMMIT;",
	} {
		if !strings.Contains(upperSQL, required) {
			t.Errorf("migration is missing %q", required)
		}
	}
	// The migration must never contain or write plaintext secrets. The app
	// password column only stores ciphertext created by the service.
	for _, forbidden := range []string{"INSERT INTO", "UPDATE INVENTORY_ACCOUNTS", "APP_PASSWORD =", "PASSWORD TEXT"} {
		if strings.Contains(upperSQL, forbidden) {
			t.Errorf("migration must not write account secrets, found %q", forbidden)
		}
	}
}
