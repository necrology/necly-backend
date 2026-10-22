-- ============================================================
-- 016: Bersihkan istilah internal "Family" dan "Slot" dari katalog
--
-- Nama produk dari seed lama masih membawa label internal seperti
-- "Spotify Premium Family - Slot Anggota". Label itu tidak boleh tampil di
-- storefront maupun dashboard admin, jadi sumber datanya ikut dibersihkan
-- (bukan hanya tampilan frontend). UUID, slug, relasi, harga, dan status
-- tidak diubah.
--
-- Aman dijalankan ulang: baris hanya diperbarui ketika copy berbeda, dan
-- pembersihan generik memakai regex yang idempoten.
-- ============================================================
BEGIN;

-- Copy final untuk delapan produk seed: nama tanpa "Family"/"Slot" dan
-- deskripsi yang menyebut "akses" alih-alih "slot".
UPDATE products AS target
SET name = source.name,
    description = source.description,
    updated_at = NOW()
FROM (VALUES
    ('0192f0a3-0000-0000-0000-000000000001'::uuid, 'Netflix Premium', 'Akses profil untuk satu anggota rumah tangga yang berbagi biaya langganan agar lebih hemat, sesuai ketentuan Netflix.'),
    ('0192f0a3-0000-0000-0000-000000000002'::uuid, 'Disney+ Standard', 'Akses profil untuk satu anggota rumah tangga yang berbagi biaya langganan agar lebih hemat, sesuai ketentuan Disney+.'),
    ('0192f0a3-0000-0000-0000-000000000003'::uuid, 'Spotify Premium', 'Akses Premium untuk anggota keluarga yang tinggal serumah agar biaya lebih hemat, melalui undangan resmi sesuai ketentuan Spotify.'),
    ('0192f0a3-0000-0000-0000-000000000004'::uuid, 'Apple Music', 'Akses anggota keluarga melalui berbagi keluarga Apple agar biaya langganan lebih hemat, sesuai ketentuan Apple.'),
    ('0192f0a3-0000-0000-0000-000000000005'::uuid, 'Microsoft 365', 'Akses anggota paket Microsoft 365 dengan ruang pribadi melalui undangan resmi agar lebih hemat, sesuai ketentuan Microsoft.'),
    ('0192f0a3-0000-0000-0000-000000000006'::uuid, 'Canva Teams', 'Akses anggota tim melalui undangan resmi agar biaya kolaborasi lebih hemat, sesuai ketentuan Canva.'),
    ('0192f0a3-0000-0000-0000-000000000007'::uuid, 'Google One 2 TB', 'Akses anggota grup keluarga untuk berbagi manfaat penyimpanan secara lebih hemat, sesuai ketentuan Google.'),
    ('0192f0a3-0000-0000-0000-000000000008'::uuid, 'Dropbox', 'Akses anggota paket Dropbox dengan ruang pribadi melalui undangan resmi agar lebih hemat, sesuai ketentuan Dropbox.')
) AS source(id, name, description)
WHERE target.id = source.id
  AND (target.name, target.description) IS DISTINCT FROM (source.name, source.description);

-- Jaring pengaman untuk produk lain yang mungkin dibuat admin dengan label
-- yang sama: buang frasa "Slot <peran>" lebih dulu, lalu sisa kata
-- "slot"/"family" dan pemisah yang menggantung.
UPDATE products AS target
SET name = cleaned.name,
    updated_at = NOW()
FROM (
    SELECT id,
           btrim(
               regexp_replace(
                   regexp_replace(
                       regexp_replace(
                           regexp_replace(name, '\s*[-–—]?\s*slot\s+(anggota\s+rumah\s+tangga|anggota|keluarga)\s*', ' ', 'gi'),
                           '\s*[-–—]?\s*\mslots?\M\s*', ' ', 'gi'
                       ),
                       '\s*[-–—]?\s*\mfamily\M\s*', ' ', 'gi'
                   ),
                   '\s{2,}', ' ', 'g'
               ),
               ' -–—'
           ) AS name
    FROM products
) AS cleaned
WHERE target.id = cleaned.id
  AND target.name IS DISTINCT FROM cleaned.name;

-- Deskripsi bebas: ganti "Slot"/"slot" dengan "Akses"/"akses" dan hapus
-- kata "Family" tanpa menyisakan spasi ganda.
UPDATE products AS target
SET description = cleaned.description,
    updated_at = NOW()
FROM (
    SELECT id,
           btrim(
               regexp_replace(
                   regexp_replace(
                       regexp_replace(
                           regexp_replace(description, '\mfamily\s+sharing\M', 'berbagi keluarga', 'gi'),
                           '\mfamily\M\s*', '', 'gi'
                       ),
                       '\mSlot\M', 'Akses', 'g'
                   ),
                   '\mslot\M', 'akses', 'g'
               ),
               E' \t\n\r'
           ) AS description
    FROM products
) AS cleaned
WHERE target.id = cleaned.id
  AND target.description IS DISTINCT FROM cleaned.description;

-- Deskripsi kategori juga memuat kata "slot" pada seed awal.
UPDATE categories AS target
SET description = 'Pilihan akses streaming video untuk anggota rumah tangga sesuai ketentuan penyedia.',
    updated_at = NOW()
WHERE target.id = '0192f0a2-0000-0000-0000-000000000001'::uuid
  AND target.description IS DISTINCT FROM 'Pilihan akses streaming video untuk anggota rumah tangga sesuai ketentuan penyedia.';

UPDATE categories AS target
SET description = cleaned.description,
    updated_at = NOW()
FROM (
    SELECT id,
           btrim(
               regexp_replace(
                   regexp_replace(description, '\mSlot\M', 'Akses', 'g'),
                   '\mslot\M', 'akses', 'g'
               ),
               E' \t\n\r'
           ) AS description
    FROM categories
) AS cleaned
WHERE target.id = cleaned.id
  AND target.description IS DISTINCT FROM cleaned.description;

COMMIT;
