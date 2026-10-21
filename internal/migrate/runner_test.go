package migrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/necly/services/internal/platform/testdb"
	"gorm.io/gorm"
)

func TestSplitStatementsHandlesPostgresQuoting(t *testing.T) {
	input := `
-- comment; with a semicolon
CREATE TABLE example (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'x') THEN
        ALTER TABLE example ADD CONSTRAINT x CHECK (id IS NOT NULL);
    END IF;
END $$;
INSERT INTO example (id) VALUES ('00000000-0000-0000-0000-000000000001');
/* block ; comment */
UPDATE example SET id = id WHERE id = '00000000-0000-0000-0000-000000000001';
`
	statements, err := SplitStatements(input)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	if len(statements) != 4 {
		t.Fatalf("expected 4 statements, got %d: %#v", len(statements), statements)
	}
	if !strings.HasPrefix(statements[1], "DO $$") {
		t.Fatalf("dollar-quoted block was split: %q", statements[1])
	}
}

func TestSplitStatementsRejectsUnterminatedInput(t *testing.T) {
	if _, err := SplitStatements("SELECT 'unterminated"); !errors.Is(err, errStatementUnterminated) {
		t.Fatalf("expected unterminated error, got %v", err)
	}
}

func TestFreshRunIsRepeatableAndSeedFree(t *testing.T) {
	db := openTestDB(t)
	dir := repoMigrations(t)

	first, err := Run(context.Background(), db, Options{Dir: dir})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Adopted {
		t.Fatal("fresh database must not be reported as adopted")
	}
	if got, want := first.Applied, []string{"001", "003", "004", "005", "006", "007", "008", "009", "010", "011", "012", "013", "014", "015", "016", "017"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("applied = %v, want %v", got, want)
	}

	var users, categories, products int64
	db.Table("users").Count(&users)
	db.Table("categories").Count(&categories)
	db.Table("products").Count(&products)
	if users+categories+products != 0 {
		t.Fatalf("production chain inserted seed rows: users=%d categories=%d products=%d", users, categories, products)
	}

	for _, item := range [][2]string{{"orders", "shipping_address"}, {"orders", "notes"}, {"categories", "image_url"}} {
		table, column := item[0], item[1]
		var count int64
		db.Raw("SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name=? AND column_name=?", table, column).Scan(&count)
		if count != 1 {
			t.Fatalf("expected column %s.%s after fresh run", table, column)
		}
	}

	before := schemaSnapshot(t, db)
	second, err := Run(context.Background(), db, Options{Dir: dir})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Fatalf("second run applied DDL: %v", second.Applied)
	}
	after := schemaSnapshot(t, db)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("second run changed the schema")
	}
}

func TestAdoptedSchemaConvergesWithFreshSchema(t *testing.T) {
	fresh := openTestDB(t)
	dir := repoMigrations(t)
	if _, err := Run(context.Background(), fresh, Options{Dir: dir}); err != nil {
		t.Fatalf("fresh run: %v", err)
	}

	adopted := openTestDB(t)
	applyBaselineWithoutLedger(t, adopted, dir)
	result, err := Run(context.Background(), adopted, Options{Dir: dir})
	if err != nil {
		t.Fatalf("adopted run: %v", err)
	}
	if !result.Adopted {
		t.Fatal("existing baseline schema was not adopted")
	}
	if !reflect.DeepEqual(result.Applied, []string{"009", "010", "011", "012", "013", "014", "015", "016", "017"}) {
		t.Fatalf("adoption should apply 009 through 017, got %v", result.Applied)
	}

	type ledgerRow struct {
		Version string
		Mode    string
	}
	var rows []ledgerRow
	if err := adopted.Raw("SELECT version, mode FROM schema_migrations ORDER BY version").Scan(&rows).Error; err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if len(rows) != 16 {
		t.Fatalf("expected 16 ledger rows, got %d: %#v", len(rows), rows)
	}
	for _, row := range rows {
		want := "adopted"
		if row.Version >= "009" {
			want = "applied"
		}
		if row.Mode != want {
			t.Fatalf("version %s mode = %s, want %s", row.Version, row.Mode, want)
		}
	}
	if rows[0].Version == "002" {
		t.Fatal("development seed 002 was recorded")
	}

	// The two databases start from different histories but must end with the
	// same required shape.
	freshSnapshot := schemaSnapshot(t, fresh)
	adoptedSnapshot := schemaSnapshot(t, adopted)
	for key, value := range freshSnapshot {
		if adoptedSnapshot[key] != value {
			t.Fatalf("schema diverged for %s: fresh=%q adopted=%q", key, value, adoptedSnapshot[key])
		}
	}
}

// Legacy installations already have inventory slots. Migration 012 must keep
// that provisioned capacity usable instead of silently capping it at the new
// default of one seat.
func TestLegacyInventoryCapacityIsPreservedBySharingLimitBackfill(t *testing.T) {
	db := openTestDB(t)
	dir := repoMigrations(t)
	applyBaselineWithoutLedger(t, db, dir)

	now := time.Now().UTC()
	categoryID, productID, accountID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if err := db.Exec(`INSERT INTO categories (id, name, slug, active, created_at, updated_at)
		VALUES (?, ?, ?, TRUE, ?, ?)`, categoryID, "Streaming", "streaming-"+categoryID[:8], now, now).Error; err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := db.Exec(`INSERT INTO products (id, category_id, name, slug, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, TRUE, ?, ?)`, productID, categoryID, "Netflix", "netflix-"+productID[:8], now, now).Error; err != nil {
		t.Fatalf("seed product: %v", err)
	}
	if err := db.Exec(`INSERT INTO inventory_accounts (id, product_id, encrypted_credentials, status, created_at, updated_at)
		VALUES (?, ?, ?, 'active', ?, ?)`, accountID, productID, []byte("cipher"), now, now).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	durationID, priceID := uuid.NewString(), uuid.NewString()
	if err := db.Exec(`INSERT INTO product_durations (id, name, days, created_at, updated_at)
		VALUES (?, '1 Bulan', 30, ?, ?)`, durationID, now, now).Error; err != nil {
		t.Fatalf("seed duration: %v", err)
	}
	if err := db.Exec(`INSERT INTO product_prices (id, product_id, duration_id, amount, currency, active, created_at, updated_at)
		VALUES (?, ?, ?, 10000, 'IDR', TRUE, ?, ?)`, priceID, productID, durationID, now, now).Error; err != nil {
		t.Fatalf("seed price: %v", err)
	}
	for index := 0; index < 3; index++ {
		if err := db.Exec(`INSERT INTO inventory_slots (id, inventory_account_id, product_price_id, status, created_at, updated_at)
			VALUES (?, ?, ?, 'available', ?, ?)`, uuid.NewString(), accountID, priceID, now, now).Error; err != nil {
			t.Fatalf("seed slot: %v", err)
		}
	}

	if _, err := Run(context.Background(), db, Options{Dir: dir}); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}

	var account struct {
		Label        string
		SharingLimit int
	}
	if err := db.Raw("SELECT label, sharing_limit FROM inventory_accounts WHERE id = ?", accountID).Scan(&account).Error; err != nil {
		t.Fatalf("read migrated account: %v", err)
	}
	// Migration 013 backfills the contract default label for rows created before
	// 012 added the column with an empty default.
	if account.Label != "Akun inventaris" {
		t.Fatalf("legacy label = %q, want %q", account.Label, "Akun inventaris")
	}
	if account.SharingLimit != 3 {
		t.Fatalf("legacy sharing_limit = %d, want 3 (one per existing slot)", account.SharingLimit)
	}

	second, err := Run(context.Background(), db, Options{Dir: dir})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(second.Applied) != 0 {
		t.Fatalf("second run applied DDL: %v", second.Applied)
	}
}

func TestChecksumMismatchAbortsBeforeDDL(t *testing.T) {
	db := openTestDB(t)
	dir := copyMigrations(t)
	if _, err := Run(context.Background(), db, Options{Dir: dir}); err != nil {
		t.Fatalf("first run: %v", err)
	}

	path := filepath.Join(dir, "009_order_compatibility.sql")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(content, []byte("\n-- tampered\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	before := schemaSnapshot(t, db)
	_, err = Run(context.Background(), db, Options{Dir: dir})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
	after := schemaSnapshot(t, db)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("checksum mismatch still executed DDL")
	}
}

func TestFailedMigrationRollsBackAndStops(t *testing.T) {
	db := openTestDB(t)
	dir := copyMigrations(t)
	writeFile(t, filepath.Join(dir, "099_failing.sql"), "BEGIN;\nCREATE TABLE should_not_survive (id int);\nSELECT 1 / 0;\nCOMMIT;\n")

	_, err := Run(context.Background(), db, Options{Dir: dir})
	if err == nil {
		t.Fatal("expected failing migration to abort the run")
	}
	var tableCount int64
	db.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='should_not_survive'").Scan(&tableCount)
	if tableCount != 0 {
		t.Fatal("failed migration left its table behind")
	}
	var ledger int64
	db.Raw("SELECT count(*) FROM schema_migrations WHERE version='099'").Scan(&ledger)
	if ledger != 0 {
		t.Fatal("failed migration was recorded as applied")
	}
}

func TestSchemaDriftIsDetected(t *testing.T) {
	db := openTestDB(t)
	dir := repoMigrations(t)
	if _, err := Run(context.Background(), db, Options{Dir: dir}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := db.Exec("ALTER TABLE orders DROP COLUMN notes").Error; err != nil {
		t.Fatal(err)
	}

	_, err := Run(context.Background(), db, Options{Dir: dir})
	var drift *SchemaDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("expected SchemaDriftError, got %v", err)
	}
	if !strings.Contains(err.Error(), "orders.notes") {
		t.Fatalf("drift report did not name orders.notes: %v", err)
	}
}

func TestAdoptionRejectsMissingBaselineObject(t *testing.T) {
	db := openTestDB(t)
	dir := repoMigrations(t)
	applyBaselineWithoutLedger(t, db, dir)
	if err := db.Exec("DROP INDEX idx_orders_status").Error; err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), db, Options{Dir: dir})
	var drift *SchemaDriftError
	if !errors.As(err, &drift) {
		t.Fatalf("expected adoption drift, got %v", err)
	}
	if !strings.Contains(err.Error(), "idx") && !strings.Contains(err.Error(), "index") {
		t.Fatalf("drift did not mention the missing index: %v", err)
	}
	var ledger int64
	db.Raw("SELECT count(*) FROM schema_migrations").Scan(&ledger)
	if ledger != 0 {
		t.Fatal("divergent schema was marked as adopted")
	}
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if !testdb.Available() {
		t.Skip("DB_HOST/DB_USER not set; skipping PostgreSQL integration test")
	}
	db, cleanup, err := testdb.Open()
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(cleanup)
	return db
}

func repoMigrations(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func copyMigrations(t *testing.T) string {
	t.Helper()
	source := repoMigrations(t)
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(target, entry.Name()), string(content))
	}
	return target
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// applyBaselineWithoutLedger simulates a database that already has the
// baseline schema but no migration ledger, such as the current production
// volume. It deliberately never runs 002.
func applyBaselineWithoutLedger(t *testing.T, db *gorm.DB, dir string) {
	t.Helper()
	definitions, err := LoadDefinitions(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.Version > "008" {
			continue
		}
		statements, err := SplitStatements(string(definition.Content))
		if err != nil {
			t.Fatal(err)
		}
		err = db.Transaction(func(tx *gorm.DB) error {
			for _, statement := range statements {
				if err := tx.Exec(statement).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("apply baseline %s: %v", definition.Filename, err)
		}
	}
}

// schemaSnapshot returns the required schema shape using the same inspection
// the runner uses, so convergence checks compare semantics, not history.
func schemaSnapshot(t *testing.T, db *gorm.DB) map[string]string {
	t.Helper()
	actual, err := inspectSchema(db)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := make(map[string]string, len(actual.columns)+len(actual.indexes)+len(actual.constraints))
	for key, column := range actual.columns {
		snapshot["column:"+key] = column.dataType + "|" + column.def + "|" + boolString(column.nullable)
	}
	for key := range actual.indexes {
		snapshot["index:"+key] = "present"
	}
	for key := range actual.constraints {
		snapshot["constraint:"+key] = "present"
	}
	return snapshot
}

func boolString(value bool) string {
	if value {
		return "nullable"
	}
	return "not-null"
}

func TestLedgerFailureRollsBackTheMigration(t *testing.T) {
	db := openTestDB(t)
	dir := copyMigrations(t)
	if _, err := Run(context.Background(), db, Options{Dir: dir}); err != nil {
		t.Fatalf("first run: %v", err)
	}
	writeFile(t, filepath.Join(dir, "099_ledger_probe.sql"), "BEGIN;\nCREATE TABLE ledger_probe (id int);\nCOMMIT;\n")

	if err := db.Exec(`CREATE FUNCTION fail_ledger_insert() RETURNS trigger AS $f$
		BEGIN RAISE EXCEPTION 'ledger unavailable'; END;
		$f$ LANGUAGE plpgsql`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER fail_ledger BEFORE INSERT ON schema_migrations FOR EACH ROW EXECUTE FUNCTION fail_ledger_insert()").Error; err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), db, Options{Dir: dir}); err == nil {
		t.Fatal("expected ledger failure to abort the migration")
	}
	var tableCount, ledgerCount int64
	db.Raw("SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='ledger_probe'").Scan(&tableCount)
	db.Raw("SELECT count(*) FROM schema_migrations WHERE version='099'").Scan(&ledgerCount)
	if tableCount != 0 || ledgerCount != 0 {
		t.Fatalf("ledger failure left DDL behind: table=%d ledger=%d", tableCount, ledgerCount)
	}
}
