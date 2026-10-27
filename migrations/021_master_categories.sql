-- ============================================================
-- 021: Master categories for the admin catalog (NEC-131).
--
-- Production may have been created before the development seed was available,
-- or an operator may have renamed/removed a category. The admin master-data
-- screen therefore needs a small guaranteed baseline without ever touching an
-- existing row.
--
-- Each slug is inserted only when no category with that slug exists. The
-- existence check deliberately ignores deleted_at: a soft-deleted row still
-- owns the unique slug, so re-inserting would either fail or create a
-- duplicate. ON CONFLICT is a second guard for the slug and primary-key uniqueness.
--
-- No UPDATE or DELETE runs here, and the statement is safe to execute again.
-- ============================================================

BEGIN;

-- The IDs match the development seed in 002. Installations that already ran
-- that seed therefore skip these rows on the primary key instead of colliding
-- on the slug, and the seeded products keep their category references.
INSERT INTO categories (id, name, slug, description, active)
SELECT seed.id, seed.name, seed.slug, seed.description, TRUE
FROM (
    VALUES
        ('0192f0a2-0000-0000-0000-000000000001'::uuid, 'Streaming Video', 'video-streaming', 'Pilihan slot streaming video untuk anggota rumah tangga sesuai ketentuan penyedia.'),
        ('0192f0a2-0000-0000-0000-000000000002'::uuid, 'Streaming Musik', 'music-streaming', 'Paket streaming musik untuk anggota keluarga yang memenuhi ketentuan penyedia.'),
        ('0192f0a2-0000-0000-0000-000000000003'::uuid, 'Produktivitas Bersama', 'productivity-suites', 'Paket produktivitas untuk anggota keluarga atau tim melalui undangan resmi.'),
        ('0192f0a2-0000-0000-0000-000000000004'::uuid, 'Penyimpanan Awan', 'cloud-storage', 'Paket penyimpanan untuk anggota keluarga atau tim sesuai ketentuan penyedia.')
) AS seed(id, name, slug, description)
WHERE NOT EXISTS (
    SELECT 1
    FROM categories existing
    WHERE existing.slug = seed.slug
)
ON CONFLICT DO NOTHING;

COMMIT;
