package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMasterCategoriesMigrationIsIdempotentAndNonDestructive re-executes the
// production insert after an operator edit and a soft delete. The migration must
// add only missing slugs, leave an edited name alone, and not resurrect a
// soft-deleted category or create a duplicate slug.
func TestMasterCategoriesMigrationIsIdempotentAndNonDestructive(t *testing.T) {
	db := openTestDB(t)
	dir := repoMigrations(t)
	if _, err := Run(context.Background(), db, Options{Dir: dir}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	contents, err := os.ReadFile(filepath.Join(dir, "021_master_categories.sql"))
	if err != nil {
		t.Fatalf("read migration 021: %v", err)
	}
	statements, err := SplitStatements(string(contents))
	if err != nil {
		t.Fatalf("parse migration 021: %v", err)
	}
	var insert string
	for _, statement := range statements {
		if strings.Contains(strings.ToUpper(statement), "INSERT INTO CATEGORIES") {
			insert = statement
			break
		}
	}
	if insert == "" {
		t.Fatal("migration 021 has no category insert statement")
	}

	var initial int64
	db.Table("categories").Count(&initial)
	if initial != 4 {
		t.Fatalf("initial master categories = %d, want 4", initial)
	}

	if err := db.Exec("UPDATE categories SET name = 'Video Pilihan' WHERE slug = 'video-streaming'").Error; err != nil {
		t.Fatalf("rename category: %v", err)
	}
	if err := db.Exec("UPDATE categories SET deleted_at = ? WHERE slug = 'music-streaming'", time.Now().UTC()).Error; err != nil {
		t.Fatalf("soft delete category: %v", err)
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := db.Exec(insert).Error; err != nil {
			t.Fatalf("re-execute migration 021 (attempt %d): %v", attempt+1, err)
		}
	}

	var total, active int64
	db.Table("categories").Count(&total)
	db.Table("categories").Where("deleted_at IS NULL").Count(&active)
	if total != 4 || active != 3 {
		t.Fatalf("categories after re-run = total %d active %d, want 4/3", total, active)
	}

	var name string
	if err := db.Raw("SELECT name FROM categories WHERE slug = 'video-streaming'").Scan(&name).Error; err != nil {
		t.Fatalf("read edited category: %v", err)
	}
	if name != "Video Pilihan" {
		t.Fatalf("edited category name = %q, want preserved", name)
	}

	type duplicate struct {
		Slug  string
		Count int64
	}
	var duplicates []duplicate
	db.Raw("SELECT slug, count(*) AS count FROM categories GROUP BY slug HAVING count(*) > 1").Scan(&duplicates)
	if len(duplicates) != 0 {
		t.Fatalf("duplicate category slugs after re-run: %#v", duplicates)
	}

	var musicDeleted bool
	db.Raw("SELECT deleted_at IS NOT NULL FROM categories WHERE slug = 'music-streaming'").Scan(&musicDeleted)
	if !musicDeleted {
		t.Fatal("soft-deleted category was resurrected by the migration")
	}
}
