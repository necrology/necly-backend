-- ============================================================
-- 006: Product labels (QA baris 32)
--
-- The storefront badges ("Paling Populer", "Rekomendasi", "Best Seller", ...)
-- are authored per product rather than derived from sales volume. This column
-- is the place the admin will set them; the storefront keeps rendering its
-- static catalogue until the admin endpoints are wired to it.
-- Idempotent.
-- ============================================================

ALTER TABLE products ADD COLUMN IF NOT EXISTS labels TEXT[] NOT NULL DEFAULT '{}';
