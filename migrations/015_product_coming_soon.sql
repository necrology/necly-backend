-- ============================================================
-- 015: Coming Soon status for products.
--
-- The storefront needs to advertise an active product before it is sellable
-- (NEC-89) without hiding it from the catalog. coming_soon is additive and
-- independent from active: active=false keeps a product hidden, while
-- active=true + coming_soon=true keeps it visible but checkout refuses it.
--
-- Every existing product defaults to FALSE, so the migration changes no
-- catalog visibility and no selling behaviour until an admin opts in.
-- ============================================================

BEGIN;

ALTER TABLE products
    ADD COLUMN IF NOT EXISTS coming_soon BOOLEAN NOT NULL DEFAULT FALSE;

-- The admin/storefront badge lookup filters active, non-deleted coming-soon
-- products by category. A partial index keeps that read bounded without
-- indexing the (much larger) ordinary product set.
CREATE INDEX IF NOT EXISTS idx_products_coming_soon_active
    ON products (category_id)
    WHERE coming_soon = TRUE AND active = TRUE AND deleted_at IS NULL;

COMMIT;
