package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// TestProductIconURLMigrationBackfillsSeedIcons runs the production chain up
// to 018, applies the development seed like a real installation, then lets
// migration 019 backfill it. It also re-executes the backfill after an admin
// edit to prove the guard preserves a custom icon and only fills blanks.
func TestProductIconURLMigrationBackfillsSeedIcons(t *testing.T) {
	db := openTestDB(t)
	dir := copyMigrations(t)

	iconPath := filepath.Join(dir, "019_product_icon_url.sql")
	iconSQL, err := os.ReadFile(iconPath)
	if err != nil {
		t.Fatalf("read migration 019: %v", err)
	}
	if err := os.Remove(iconPath); err != nil {
		t.Fatalf("stage migrations before 019: %v", err)
	}
	if _, err := Run(context.Background(), db, Options{Dir: dir, SkipSchemaCheck: true}); err != nil {
		t.Fatalf("migrate through 018: %v", err)
	}

	seed, err := os.ReadFile(filepath.Join(repoMigrations(t), "002_seed_data.sql"))
	if err != nil {
		t.Fatalf("read development seed: %v", err)
	}
	var seedSQL strings.Builder
	for _, line := range strings.Split(string(seed), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		seedSQL.WriteString(line)
		seedSQL.WriteByte('\n')
	}
	statements, err := SplitStatements(seedSQL.String())
	if err != nil {
		t.Fatalf("parse development seed: %v", err)
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("apply development seed: %v", err)
		}
	}

	if err := os.WriteFile(iconPath, iconSQL, 0o644); err != nil {
		t.Fatalf("restore migration 019: %v", err)
	}
	result, err := Run(context.Background(), db, Options{Dir: dir})
	if err != nil {
		t.Fatalf("apply migration 019: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != "019" {
		t.Fatalf("applied = %v, want only 019", result.Applied)
	}

	want := map[string]string{
		"netflix-premium":            "/design-assets/logo-netflix.png",
		"disney-plus-standard":       "/design-assets/logo-disney.png",
		"spotify-premium-individual": "/design-assets/logo-spotify.png",
		"microsoft-365-personal":     "/design-assets/logo-microsoft-365.png",
		"apple-music-individual":     "",
		"canva-pro":                  "",
		"google-one-2tb":             "",
		"dropbox-plus":               "",
	}
	assertSeedIcons(t, db, want)

	// An administrator-set icon must survive a later re-run, while a blank one
	// is treated as missing and backfilled again.
	if err := db.Exec("UPDATE products SET icon_url = '/custom/netflix.png' WHERE slug = 'netflix-premium'").Error; err != nil {
		t.Fatalf("set custom icon: %v", err)
	}
	if err := db.Exec("UPDATE products SET icon_url = '   ' WHERE slug = 'disney-plus-standard'").Error; err != nil {
		t.Fatalf("blank icon: %v", err)
	}
	repeat, err := SplitStatements(string(iconSQL))
	if err != nil {
		t.Fatalf("parse migration 019: %v", err)
	}
	for _, statement := range repeat {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("re-execute migration 019: %v", err)
		}
	}
	want["netflix-premium"] = "/custom/netflix.png"
	assertSeedIcons(t, db, want)
}

func assertSeedIcons(t *testing.T, db *gorm.DB, want map[string]string) {
	t.Helper()
	type row struct {
		Slug    string
		IconURL *string
	}
	var rows []row
	if err := db.Raw(`SELECT slug, icon_url FROM products WHERE slug IN (
		'netflix-premium', 'disney-plus-standard', 'spotify-premium-individual',
		'microsoft-365-personal', 'apple-music-individual', 'canva-pro',
		'google-one-2tb', 'dropbox-plus'
	)`).Scan(&rows).Error; err != nil {
		t.Fatalf("read product icons: %v", err)
	}
	if len(rows) != len(want) {
		t.Fatalf("read %d products, want %d", len(rows), len(want))
	}
	for _, row := range rows {
		got := ""
		if row.IconURL != nil {
			got = *row.IconURL
		}
		if got != want[row.Slug] {
			t.Errorf("%s icon_url = %q, want %q", row.Slug, got, want[row.Slug])
		}
	}
}
