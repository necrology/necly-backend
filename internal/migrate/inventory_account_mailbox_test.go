package migrate

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestInventoryAccountMailboxMigration verifies migration 020 on a fresh
// database: the three columns exist with the documented defaults, the
// case-insensitive unique index is present, and the app password is nullable.
func TestInventoryAccountMailboxMigration(t *testing.T) {
	db := openTestDB(t)
	if _, err := Run(context.Background(), db, Options{Dir: repoMigrations(t)}); err != nil {
		t.Fatalf("fresh migrate: %v", err)
	}

	type column struct {
		ColumnName string
		DataType   string
		IsNullable string
		Default    *string
	}
	var columns []column
	if err := db.Raw(`SELECT column_name, data_type, is_nullable, column_default AS "default"
		FROM information_schema.columns
		WHERE table_schema='public' AND table_name='inventory_accounts'
			AND column_name IN ('account_email','mailbox_app_password_encrypted','mailbox_imap_host')
		ORDER BY column_name`).Scan(&columns).Error; err != nil {
		t.Fatalf("read columns: %v", err)
	}
	if len(columns) != 3 {
		t.Fatalf("found %d mailbox columns, want 3: %+v", len(columns), columns)
	}
	byName := make(map[string]column, len(columns))
	for _, item := range columns {
		byName[item.ColumnName] = item
	}
	if got := byName["account_email"]; got.DataType != "text" || got.IsNullable != "NO" || got.Default == nil || *got.Default != "''::text" {
		t.Fatalf("account_email shape = %+v", got)
	}
	if got := byName["mailbox_app_password_encrypted"]; got.DataType != "bytea" || got.IsNullable != "YES" || got.Default != nil {
		t.Fatalf("mailbox_app_password_encrypted shape = %+v", got)
	}
	if got := byName["mailbox_imap_host"]; got.DataType != "text" || got.IsNullable != "NO" || got.Default == nil || *got.Default != "'imap.gmail.com'::text" {
		t.Fatalf("mailbox_imap_host shape = %+v", got)
	}

	var indexDefinition string
	if err := db.Raw(`SELECT indexdef FROM pg_indexes
		WHERE schemaname='public' AND tablename='inventory_accounts'
			AND indexname='idx_inventory_accounts_account_email'`).Scan(&indexDefinition).Error; err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !strings.Contains(indexDefinition, "UNIQUE") || !strings.Contains(strings.ToLower(indexDefinition), "lower(account_email)") {
		t.Fatalf("index definition = %q", indexDefinition)
	}

	now := time.Now().UTC()
	categoryID, productID := uuid.NewString(), uuid.NewString()
	if err := db.Exec(`INSERT INTO categories (id, name, slug, active, created_at, updated_at)
		VALUES (?, 'Streaming', ?, TRUE, ?, ?)`, categoryID, "streaming-"+categoryID[:8], now, now).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := db.Exec(`INSERT INTO products (id, category_id, name, slug, active, created_at, updated_at)
		VALUES (?, ?, 'Netflix', ?, TRUE, ?, ?)`, productID, categoryID, "netflix-"+productID[:8], now, now).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	firstID := uuid.NewString()
	if err := db.Exec(`INSERT INTO inventory_accounts (id, product_id, encrypted_credentials, account_email, created_at, updated_at)
		VALUES (?, ?, ?, 'family@example.test', ?, ?)`, firstID, productID, []byte("cipher"), now, now).Error; err != nil {
		t.Fatalf("insert account: %v", err)
	}
	var storedEmail, storedHost string
	var nullPassword int64
	if err := db.Raw(`SELECT account_email FROM inventory_accounts WHERE id = ?`, firstID).Scan(&storedEmail).Error; err != nil {
		t.Fatalf("read account email: %v", err)
	}
	if err := db.Raw(`SELECT mailbox_imap_host FROM inventory_accounts WHERE id = ?`, firstID).Scan(&storedHost).Error; err != nil {
		t.Fatalf("read mailbox host: %v", err)
	}
	if err := db.Raw(`SELECT count(*) FROM inventory_accounts WHERE id = ? AND mailbox_app_password_encrypted IS NULL`, firstID).Scan(&nullPassword).Error; err != nil {
		t.Fatalf("read mailbox app password: %v", err)
	}
	if storedEmail != "family@example.test" || storedHost != "imap.gmail.com" || nullPassword != 1 {
		t.Fatalf("stored account = email %q host %q null-app-password=%d", storedEmail, storedHost, nullPassword)
	}

	// The unique index compares lower(account_email), so another case variant is
	// a conflict while two empty legacy addresses remain allowed.
	if err := db.Exec(`INSERT INTO inventory_accounts (id, product_id, encrypted_credentials, account_email, created_at, updated_at)
		VALUES (?, ?, ?, 'FAMILY@example.test', ?, ?)`, uuid.NewString(), productID, []byte("cipher"), now, now).Error; err == nil {
		t.Fatal("case-variant email bypassed the unique index")
	}
	for i := 0; i < 2; i++ {
		if err := db.Exec(`INSERT INTO inventory_accounts (id, product_id, encrypted_credentials, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)`, uuid.NewString(), productID, []byte("cipher"), now, now).Error; err != nil {
			t.Fatalf("empty legacy email %d rejected: %v", i, err)
		}
	}
}
