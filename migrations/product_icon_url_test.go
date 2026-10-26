package migrations

import (
	"strings"
	"testing"
)

func TestProductIconURLMigrationOnlyBackfillsMatchingAssets(t *testing.T) {
	sql := readMigration(t, "019_product_icon_url.sql")
	upperSQL := strings.ToUpper(sql)

	for _, required := range []string{
		"ALTER TABLE PRODUCTS",
		"ADD COLUMN IF NOT EXISTS ICON_URL TEXT",
		"AND (ICON_URL IS NULL OR BTRIM(ICON_URL) = '')",
		"WHEN 'NETFLIX-PREMIUM' THEN '/DESIGN-ASSETS/LOGO-NETFLIX.PNG'",
		"WHEN 'DISNEY-PLUS-STANDARD' THEN '/DESIGN-ASSETS/LOGO-DISNEY.PNG'",
		"WHEN 'SPOTIFY-PREMIUM-INDIVIDUAL' THEN '/DESIGN-ASSETS/LOGO-SPOTIFY.PNG'",
		"WHEN 'MICROSOFT-365-PERSONAL' THEN '/DESIGN-ASSETS/LOGO-MICROSOFT-365.PNG'",
	} {
		if !strings.Contains(upperSQL, required) {
			t.Errorf("migration is missing %q", required)
		}
	}
	// Apple One's logo is not Apple Music's logo, and the other products have
	// no matching public asset yet, so they must keep icon_url NULL.
	if strings.Contains(upperSQL, "THEN '/DESIGN-ASSETS/LOGO-APPLE-ONE.PNG'") {
		t.Error("migration must not use logo-apple-one.png for Apple Music")
	}
	if count := strings.Count(upperSQL, "WHEN '"); count != 4 {
		t.Errorf("migration must backfill exactly four seed products, got %d", count)
	}
	if count := strings.Count(upperSQL, "UPDATE PRODUCTS"); count != 1 {
		t.Errorf("migration must use one scoped product update, got %d", count)
	}
}
