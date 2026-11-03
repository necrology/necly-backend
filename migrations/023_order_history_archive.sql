-- ============================================================
-- 023: Archive riwayat pesanan (NEC-160 / QA mobile baris 114).
--
-- Menyembunyikan pesanan terminal dari daftar riwayat pelanggan tanpa
-- menghapus pesanan, pembayaran, pengiriman, inventaris, atau audit. Arsip
-- bersifat lunak dan idempoten: archived_at hanya diisi sekali.
--
-- Aditif dan idempoten: aman dijalankan ulang.
-- ============================================================

BEGIN;

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_orders_user_archived_at
    ON orders (user_id, archived_at);

COMMIT;
