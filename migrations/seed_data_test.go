package migrations

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func readMigration(t *testing.T, name string) string {
	t.Helper()

	contents, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(contents)
}

func TestDevelopmentSeedUsesIndonesianSharingCatalogCopy(t *testing.T) {
	sql := readMigration(t, "002_seed_data.sql")

	durations := []struct {
		id   string
		name string
		days int
	}{
		{"0192f0a1-0000-0000-0000-000000000001", "1 Bulan", 30},
		{"0192f0a1-0000-0000-0000-000000000002", "3 Bulan", 90},
		{"0192f0a1-0000-0000-0000-000000000003", "6 Bulan", 180},
		{"0192f0a1-0000-0000-0000-000000000004", "12 Bulan", 365},
	}
	for _, duration := range durations {
		t.Run("duration_"+duration.name, func(t *testing.T) {
			want := fmt.Sprintf("('%s', '%s', %d)", duration.id, duration.name, duration.days)
			if !strings.Contains(sql, want) {
				t.Errorf("seed must contain localized duration %q", want)
			}
		})
	}

	categories := []struct {
		id          string
		name        string
		slug        string
		description string
	}{
		{"0192f0a2-0000-0000-0000-000000000001", "Streaming Video", "video-streaming", "Pilihan slot streaming video untuk anggota rumah tangga sesuai ketentuan penyedia."},
		{"0192f0a2-0000-0000-0000-000000000002", "Streaming Musik", "music-streaming", "Paket streaming musik untuk anggota keluarga yang memenuhi ketentuan penyedia."},
		{"0192f0a2-0000-0000-0000-000000000003", "Produktivitas Bersama", "productivity-suites", "Paket produktivitas untuk anggota keluarga atau tim melalui undangan resmi."},
		{"0192f0a2-0000-0000-0000-000000000004", "Penyimpanan Awan", "cloud-storage", "Paket penyimpanan untuk anggota keluarga atau tim sesuai ketentuan penyedia."},
	}
	for _, category := range categories {
		t.Run("category_"+category.slug, func(t *testing.T) {
			want := fmt.Sprintf("('%s', '%s', '%s', '%s', TRUE)", category.id, category.name, category.slug, category.description)
			if !strings.Contains(sql, want) {
				t.Errorf("seed must contain localized category while preserving its ID and slug: %q", want)
			}
		})
	}

	products := []struct {
		id          string
		categoryID  string
		name        string
		slug        string
		description string
	}{
		{"0192f0a3-0000-0000-0000-000000000001", "0192f0a2-0000-0000-0000-000000000001", "Netflix Premium - Slot Anggota Rumah Tangga", "netflix-premium", "Slot profil untuk anggota satu rumah tangga yang berbagi biaya langganan agar lebih hemat, sesuai ketentuan Netflix."},
		{"0192f0a3-0000-0000-0000-000000000002", "0192f0a2-0000-0000-0000-000000000001", "Disney+ Standard - Slot Anggota Rumah Tangga", "disney-plus-standard", "Slot profil untuk anggota satu rumah tangga yang berbagi biaya langganan agar lebih hemat, sesuai ketentuan Disney+."},
		{"0192f0a3-0000-0000-0000-000000000003", "0192f0a2-0000-0000-0000-000000000002", "Spotify Premium Family - Slot Anggota", "spotify-premium-individual", "Slot Premium Family untuk anggota keluarga yang tinggal serumah agar biaya lebih hemat, melalui undangan resmi sesuai ketentuan Spotify."},
		{"0192f0a3-0000-0000-0000-000000000004", "0192f0a2-0000-0000-0000-000000000002", "Apple Music Family - Slot Anggota", "apple-music-individual", "Slot anggota keluarga melalui Family Sharing agar biaya langganan lebih hemat, sesuai ketentuan Apple."},
		{"0192f0a3-0000-0000-0000-000000000005", "0192f0a2-0000-0000-0000-000000000003", "Microsoft 365 Family - Slot Anggota", "microsoft-365-personal", "Slot anggota paket Family dengan akses pribadi melalui undangan resmi agar lebih hemat, sesuai ketentuan Microsoft."},
		{"0192f0a3-0000-0000-0000-000000000006", "0192f0a2-0000-0000-0000-000000000003", "Canva Teams - Slot Anggota", "canva-pro", "Slot anggota tim melalui undangan resmi agar biaya kolaborasi lebih hemat, sesuai ketentuan Canva."},
		{"0192f0a3-0000-0000-0000-000000000007", "0192f0a2-0000-0000-0000-000000000004", "Google One 2 TB - Slot Keluarga", "google-one-2tb", "Slot anggota grup keluarga untuk berbagi manfaat penyimpanan secara lebih hemat, sesuai ketentuan Google."},
		{"0192f0a3-0000-0000-0000-000000000008", "0192f0a2-0000-0000-0000-000000000004", "Dropbox Family - Slot Anggota", "dropbox-plus", "Slot anggota paket Family dengan ruang pribadi melalui undangan resmi agar lebih hemat, sesuai ketentuan Dropbox."},
	}
	for _, product := range products {
		t.Run("product_"+product.slug, func(t *testing.T) {
			want := fmt.Sprintf("('%s', '%s', '%s', '%s', '%s', TRUE)", product.id, product.categoryID, product.name, product.slug, product.description)
			if !strings.Contains(sql, want) {
				t.Errorf("seed must contain sharing-plan copy while preserving its IDs and slug: %q", want)
			}
		})
	}

	forbiddenCopy := []string{
		"'Monthly'", "'Quarterly'", "'Semi-Annual'", "'Annual'",
		"'Video Streaming'", "'Music Streaming'", "'Productivity Suites'", "'Cloud Storage'",
		"'Spotify Premium Individual'", "'Apple Music Individual'", "'Microsoft 365 Personal'",
		"'Premium video streaming subscriptions'", "'High-quality music streaming plans'",
		"'Office and collaboration software subscriptions'", "'Personal and team cloud storage plans'",
		"'4K HDR streaming on up to 4 devices'", "'HD streaming on 2 devices'",
		"'Ad-free music, offline listening, high quality'", "'Lossless audio, spatial audio, offline'",
		"'Office apps, 1TB OneDrive, premium features'", "'Premium templates, brand kit, background remover'",
		"'2TB storage across Drive, Gmail, Photos'", "'2TB storage, smart sync, file recovery'",
	}
	for _, forbidden := range forbiddenCopy {
		if strings.Contains(sql, forbidden) {
			t.Errorf("seed still contains obsolete English or individual-plan copy %s", forbidden)
		}
	}
}

func TestExistingCatalogCopyMigrationIsScopedAndIdempotent(t *testing.T) {
	sql := readMigration(t, "003_localize_catalog_copy.sql")
	upperSQL := strings.ToUpper(sql)

	for _, forbiddenStatement := range []string{"INSERT INTO", "DELETE FROM", "ALTER TABLE", "DROP TABLE", "TRUNCATE"} {
		if strings.Contains(upperSQL, forbiddenStatement) {
			t.Errorf("catalog copy migration must use scoped updates, found %q", forbiddenStatement)
		}
	}
	for _, untouchedColumn := range []string{"slug =", "category_id =", "days =", "amount ="} {
		if strings.Contains(strings.ToLower(sql), untouchedColumn) {
			t.Errorf("catalog copy migration must not change stable contract or pricing column %q", untouchedColumn)
		}
	}
	if got := strings.Count(upperSQL, "IS DISTINCT FROM"); got != 3 {
		t.Errorf("migration must guard all three updates for safe re-runs; got %d guards", got)
	}

	required := []string{
		"BEGIN;",
		"UPDATE product_durations AS target",
		"('0192f0a1-0000-0000-0000-000000000001'::uuid, '1 Bulan')",
		"('0192f0a1-0000-0000-0000-000000000002'::uuid, '3 Bulan')",
		"('0192f0a1-0000-0000-0000-000000000003'::uuid, '6 Bulan')",
		"('0192f0a1-0000-0000-0000-000000000004'::uuid, '12 Bulan')",
		"UPDATE categories AS target",
		"('0192f0a2-0000-0000-0000-000000000001'::uuid, 'Streaming Video', 'Pilihan slot streaming video untuk anggota rumah tangga sesuai ketentuan penyedia.')",
		"('0192f0a2-0000-0000-0000-000000000002'::uuid, 'Streaming Musik', 'Paket streaming musik untuk anggota keluarga yang memenuhi ketentuan penyedia.')",
		"('0192f0a2-0000-0000-0000-000000000003'::uuid, 'Produktivitas Bersama', 'Paket produktivitas untuk anggota keluarga atau tim melalui undangan resmi.')",
		"('0192f0a2-0000-0000-0000-000000000004'::uuid, 'Penyimpanan Awan', 'Paket penyimpanan untuk anggota keluarga atau tim sesuai ketentuan penyedia.')",
		"UPDATE products AS target",
		"('0192f0a3-0000-0000-0000-000000000001'::uuid, 'Netflix Premium - Slot Anggota Rumah Tangga', 'Slot profil untuk anggota satu rumah tangga yang berbagi biaya langganan agar lebih hemat, sesuai ketentuan Netflix.')",
		"('0192f0a3-0000-0000-0000-000000000002'::uuid, 'Disney+ Standard - Slot Anggota Rumah Tangga', 'Slot profil untuk anggota satu rumah tangga yang berbagi biaya langganan agar lebih hemat, sesuai ketentuan Disney+.')",
		"('0192f0a3-0000-0000-0000-000000000003'::uuid, 'Spotify Premium Family - Slot Anggota', 'Slot Premium Family untuk anggota keluarga yang tinggal serumah agar biaya lebih hemat, melalui undangan resmi sesuai ketentuan Spotify.')",
		"('0192f0a3-0000-0000-0000-000000000004'::uuid, 'Apple Music Family - Slot Anggota', 'Slot anggota keluarga melalui Family Sharing agar biaya langganan lebih hemat, sesuai ketentuan Apple.')",
		"('0192f0a3-0000-0000-0000-000000000005'::uuid, 'Microsoft 365 Family - Slot Anggota', 'Slot anggota paket Family dengan akses pribadi melalui undangan resmi agar lebih hemat, sesuai ketentuan Microsoft.')",
		"('0192f0a3-0000-0000-0000-000000000006'::uuid, 'Canva Teams - Slot Anggota', 'Slot anggota tim melalui undangan resmi agar biaya kolaborasi lebih hemat, sesuai ketentuan Canva.')",
		"('0192f0a3-0000-0000-0000-000000000007'::uuid, 'Google One 2 TB - Slot Keluarga', 'Slot anggota grup keluarga untuk berbagi manfaat penyimpanan secara lebih hemat, sesuai ketentuan Google.')",
		"('0192f0a3-0000-0000-0000-000000000008'::uuid, 'Dropbox Family - Slot Anggota', 'Slot anggota paket Family dengan ruang pribadi melalui undangan resmi agar lebih hemat, sesuai ketentuan Dropbox.')",
		"COMMIT;",
	}
	for _, snippet := range required {
		if !strings.Contains(sql, snippet) {
			t.Errorf("catalog copy migration is missing %q", snippet)
		}
	}
}

func TestUsersActiveMigrationIsSafeToRerun(t *testing.T) {
	sql := readMigration(t, "008_add_users_active.sql")
	upperSQL := strings.ToUpper(sql)

	for _, required := range []string{
		"BEGIN;",
		"ADD COLUMN IF NOT EXISTS ACTIVE BOOLEAN NOT NULL DEFAULT TRUE",
		"CREATE INDEX IF NOT EXISTS IDX_USERS_ACTIVE ON USERS(ACTIVE)",
		"COMMIT;",
	} {
		if !strings.Contains(upperSQL, required) {
			t.Errorf("users active migration is missing %q", required)
		}
	}
}
