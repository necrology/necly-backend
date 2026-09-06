-- Perbarui copy katalog seed lama tanpa mengubah UUID, slug, relasi, harga, atau status.
-- Aman dijalankan ulang: baris hanya diperbarui ketika copy berbeda.
BEGIN;

UPDATE product_durations AS target
SET name = source.name,
    updated_at = NOW()
FROM (VALUES
    ('0192f0a1-0000-0000-0000-000000000001'::uuid, '1 Bulan'),
    ('0192f0a1-0000-0000-0000-000000000002'::uuid, '3 Bulan'),
    ('0192f0a1-0000-0000-0000-000000000003'::uuid, '6 Bulan'),
    ('0192f0a1-0000-0000-0000-000000000004'::uuid, '12 Bulan')
) AS source(id, name)
WHERE target.id = source.id
  AND target.name IS DISTINCT FROM source.name;

UPDATE categories AS target
SET name = source.name,
    description = source.description,
    updated_at = NOW()
FROM (VALUES
    ('0192f0a2-0000-0000-0000-000000000001'::uuid, 'Streaming Video', 'Pilihan slot streaming video untuk anggota rumah tangga sesuai ketentuan penyedia.'),
    ('0192f0a2-0000-0000-0000-000000000002'::uuid, 'Streaming Musik', 'Paket streaming musik untuk anggota keluarga yang memenuhi ketentuan penyedia.'),
    ('0192f0a2-0000-0000-0000-000000000003'::uuid, 'Produktivitas Bersama', 'Paket produktivitas untuk anggota keluarga atau tim melalui undangan resmi.'),
    ('0192f0a2-0000-0000-0000-000000000004'::uuid, 'Penyimpanan Awan', 'Paket penyimpanan untuk anggota keluarga atau tim sesuai ketentuan penyedia.')
) AS source(id, name, description)
WHERE target.id = source.id
  AND (target.name, target.description) IS DISTINCT FROM (source.name, source.description);

UPDATE products AS target
SET name = source.name,
    description = source.description,
    updated_at = NOW()
FROM (VALUES
    ('0192f0a3-0000-0000-0000-000000000001'::uuid, 'Netflix Premium - Slot Anggota Rumah Tangga', 'Slot profil untuk anggota satu rumah tangga yang berbagi biaya langganan agar lebih hemat, sesuai ketentuan Netflix.'),
    ('0192f0a3-0000-0000-0000-000000000002'::uuid, 'Disney+ Standard - Slot Anggota Rumah Tangga', 'Slot profil untuk anggota satu rumah tangga yang berbagi biaya langganan agar lebih hemat, sesuai ketentuan Disney+.'),
    ('0192f0a3-0000-0000-0000-000000000003'::uuid, 'Spotify Premium Family - Slot Anggota', 'Slot Premium Family untuk anggota keluarga yang tinggal serumah agar biaya lebih hemat, melalui undangan resmi sesuai ketentuan Spotify.'),
    ('0192f0a3-0000-0000-0000-000000000004'::uuid, 'Apple Music Family - Slot Anggota', 'Slot anggota keluarga melalui Family Sharing agar biaya langganan lebih hemat, sesuai ketentuan Apple.'),
    ('0192f0a3-0000-0000-0000-000000000005'::uuid, 'Microsoft 365 Family - Slot Anggota', 'Slot anggota paket Family dengan akses pribadi melalui undangan resmi agar lebih hemat, sesuai ketentuan Microsoft.'),
    ('0192f0a3-0000-0000-0000-000000000006'::uuid, 'Canva Teams - Slot Anggota', 'Slot anggota tim melalui undangan resmi agar biaya kolaborasi lebih hemat, sesuai ketentuan Canva.'),
    ('0192f0a3-0000-0000-0000-000000000007'::uuid, 'Google One 2 TB - Slot Keluarga', 'Slot anggota grup keluarga untuk berbagi manfaat penyimpanan secara lebih hemat, sesuai ketentuan Google.'),
    ('0192f0a3-0000-0000-0000-000000000008'::uuid, 'Dropbox Family - Slot Anggota', 'Slot anggota paket Family dengan ruang pribadi melalui undangan resmi agar lebih hemat, sesuai ketentuan Dropbox.')
) AS source(id, name, description)
WHERE target.id = source.id
  AND (target.name, target.description) IS DISTINCT FROM (source.name, source.description);

COMMIT;
