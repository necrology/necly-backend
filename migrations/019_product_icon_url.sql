-- ============================================================
-- 019: Product icon URL.
--
-- Products already have image_url for the catalog photo. The storefront also
-- shows a public brand icon that used to be hardcoded in the frontend; storing
-- it lets administrators change the icon per product without a deploy.
--
-- Only seed products with a matching public asset are backfilled. Apple Music,
-- Canva, Google One and Dropbox deliberately stay NULL until the correct asset
-- is available (logo-apple-one.png is Apple One, not Apple Music).
--
-- The backfill never overwrites an icon an administrator already set, so this
-- migration is safe to run more than once.
-- ============================================================

BEGIN;

ALTER TABLE products
    ADD COLUMN IF NOT EXISTS icon_url TEXT;

UPDATE products
SET icon_url = CASE slug
    WHEN 'netflix-premium' THEN '/design-assets/logo-netflix.png'
    WHEN 'disney-plus-standard' THEN '/design-assets/logo-disney.png'
    WHEN 'spotify-premium-individual' THEN '/design-assets/logo-spotify.png'
    WHEN 'microsoft-365-personal' THEN '/design-assets/logo-microsoft-365.png'
END
WHERE slug IN (
        'netflix-premium',
        'disney-plus-standard',
        'spotify-premium-individual',
        'microsoft-365-personal'
    )
    AND (icon_url IS NULL OR btrim(icon_url) = '');

COMMIT;
