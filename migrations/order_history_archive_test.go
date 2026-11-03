package migrations

import (
	"strings"
	"testing"
)

func TestOrderHistoryArchiveMigrationIsAdditiveAndIndexed(t *testing.T) {
	sql := readMigration(t, "023_order_history_archive.sql")
	upperSQL := strings.ToUpper(sql)

	for _, required := range []string{
		"BEGIN;",
		"ALTER TABLE ORDERS",
		"ADD COLUMN IF NOT EXISTS ARCHIVED_AT TIMESTAMPTZ",
		"CREATE INDEX IF NOT EXISTS IDX_ORDERS_USER_ARCHIVED_AT",
		"ON ORDERS (USER_ID, ARCHIVED_AT)",
		"COMMIT;",
	} {
		if !strings.Contains(upperSQL, required) {
			t.Errorf("migration is missing %q", required)
		}
	}

	// Archiving is a soft, reversible hide. The migration must not delete,
	// rewrite, or backfill financial and delivery history.
	for _, forbidden := range []string{
		"DELETE FROM", "DROP TABLE", "DROP COLUMN", "TRUNCATE",
		"UPDATE ORDERS", "UPDATE PAYMENTS", "UPDATE ORDER_DELIVERIES",
		"UPDATE INVENTORY_SLOTS", "ALTER COLUMN",
	} {
		if strings.Contains(upperSQL, forbidden) {
			t.Errorf("migration must be additive only, found %q", forbidden)
		}
	}
}
