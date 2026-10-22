package migrations

import (
	"strings"
	"testing"
)

// NEC-95: nama produk yang tampil di storefront dan dashboard admin tidak boleh
// lagi membawa kata "Family" atau "Slot" dari seed lama. Migrasi ini
// membersihkan sumber datanya, bukan hanya tampilan frontend.
func TestCleanProductNamesMigrationRemovesInternalLabels(t *testing.T) {
	sql := readMigration(t, "016_clean_product_names.sql")

	required := []string{
		"BEGIN;",
		"UPDATE products AS target",
		"('0192f0a3-0000-0000-0000-000000000001'::uuid, 'Netflix Premium',",
		"('0192f0a3-0000-0000-0000-000000000003'::uuid, 'Spotify Premium',",
		"('0192f0a3-0000-0000-0000-000000000004'::uuid, 'Apple Music',",
		"('0192f0a3-0000-0000-0000-000000000005'::uuid, 'Microsoft 365',",
		"('0192f0a3-0000-0000-0000-000000000007'::uuid, 'Google One 2 TB',",
		"('0192f0a3-0000-0000-0000-000000000008'::uuid, 'Dropbox',",
		"family",
		"slots?",
		"UPDATE categories AS target",
		"IS DISTINCT FROM",
		"COMMIT;",
	}

	lower := strings.ToLower(sql)
	for _, snippet := range required {
		if !strings.Contains(lower, strings.ToLower(snippet)) {
			t.Errorf("migration copy is missing %q", snippet)
		}
	}
}

// Nilai nama pada blok VALUES tidak boleh kembali memuat label internal.
func TestCleanProductNamesMigrationCatalogValuesHaveNoInternalLabels(t *testing.T) {
	sql := readMigration(t, "016_clean_product_names.sql")

	start := strings.Index(sql, "FROM (VALUES")
	end := strings.Index(sql, ") AS source(id, name, description)")
	if start < 0 || end < 0 || end <= start {
		t.Fatal("cannot locate the explicit product copy block")
	}

	for _, line := range strings.Split(sql[start:end], "\n") {
		if !strings.Contains(line, "::uuid") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, " family") || strings.Contains(lower, "slot ") || strings.Contains(lower, "- slot") {
			t.Errorf("product copy still contains an internal label: %s", strings.TrimSpace(line))
		}
	}
}
