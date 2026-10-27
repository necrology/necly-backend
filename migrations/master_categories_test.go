package migrations

import (
	"strings"
	"testing"
)

func TestMasterCategoriesMigrationIsIdempotentAndNonDestructive(t *testing.T) {
	sql := readMigration(t, "021_master_categories.sql")
	upperSQL := strings.ToUpper(sql)

	for _, required := range []string{
		"BEGIN;",
		"INSERT INTO CATEGORIES (ID, NAME, SLUG, DESCRIPTION, ACTIVE)",
		"WHERE NOT EXISTS",
		"EXISTING.SLUG = SEED.SLUG",
		"ON CONFLICT DO NOTHING",
		"COMMIT;",
	} {
		if !strings.Contains(upperSQL, required) {
			t.Errorf("migration is missing %q", required)
		}
	}

	categories := []struct {
		id   string
		name string
		slug string
	}{
		{"0192f0a2-0000-0000-0000-000000000001", "Streaming Video", "video-streaming"},
		{"0192f0a2-0000-0000-0000-000000000002", "Streaming Musik", "music-streaming"},
		{"0192f0a2-0000-0000-0000-000000000003", "Produktivitas Bersama", "productivity-suites"},
		{"0192f0a2-0000-0000-0000-000000000004", "Penyimpanan Awan", "cloud-storage"},
	}
	for _, category := range categories {
		want := "('" + category.id + "'::uuid, '" + category.name + "', '" + category.slug + "',"
		if !strings.Contains(sql, want) {
			t.Errorf("migration must seed %q", want)
		}
	}
	if count := strings.Count(upperSQL, "('"); count != len(categories) {
		t.Errorf("migration must seed exactly %d categories, got %d", len(categories), count)
	}

	// The slug guard must not filter soft-deleted rows: a deleted category still
	// owns the unique slug and must not be duplicated.
	if strings.Contains(upperSQL, "DELETED_AT IS NULL") {
		t.Error("existence check must include soft-deleted categories so the unique slug is not duplicated")
	}
	// The migration is additive only. Updating or deleting an existing category
	// would overwrite operator changes.
	for _, forbidden := range []string{"UPDATE CATEGORIES", "DELETE FROM CATEGORIES", "TRUNCATE"} {
		if strings.Contains(upperSQL, forbidden) {
			t.Errorf("migration must not modify existing categories, found %q", forbidden)
		}
	}
	if count := strings.Count(upperSQL, "INSERT INTO CATEGORIES"); count != 1 {
		t.Errorf("migration must use one insert statement, got %d", count)
	}
}
