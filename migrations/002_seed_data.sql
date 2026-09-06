-- Necly Services - Development Seed Data
-- Run this AFTER 001_initial_schema.sql on a DEVELOPMENT database ONLY.
-- DO NOT RUN IN PRODUCTION. This file contains no real credentials or secrets.
-- Inventory accounts with encrypted credentials must be created via the admin API
-- using the CREDENTIAL_ENCRYPTION_KEY from environment.

-- ============================================================
-- Durasi Produk
-- ============================================================
INSERT INTO product_durations (id, name, days) VALUES
    ('0192f0a1-0000-0000-0000-000000000001', '1 Bulan', 30),
    ('0192f0a1-0000-0000-0000-000000000002', '3 Bulan', 90),
    ('0192f0a1-0000-0000-0000-000000000003', '6 Bulan', 180),
    ('0192f0a1-0000-0000-0000-000000000004', '12 Bulan', 365)
ON CONFLICT (id) DO NOTHING;

-- ============================================================
-- Kategori Layanan Berlangganan Bersama
-- ============================================================
INSERT INTO categories (id, name, slug, description, active) VALUES
    ('0192f0a2-0000-0000-0000-000000000001', 'Streaming Video', 'video-streaming', 'Pilihan slot streaming video untuk anggota rumah tangga sesuai ketentuan penyedia.', TRUE),
    ('0192f0a2-0000-0000-0000-000000000002', 'Streaming Musik', 'music-streaming', 'Paket streaming musik untuk anggota keluarga yang memenuhi ketentuan penyedia.', TRUE),
    ('0192f0a2-0000-0000-0000-000000000003', 'Produktivitas Bersama', 'productivity-suites', 'Paket produktivitas untuk anggota keluarga atau tim melalui undangan resmi.', TRUE),
    ('0192f0a2-0000-0000-0000-000000000004', 'Penyimpanan Awan', 'cloud-storage', 'Paket penyimpanan untuk anggota keluarga atau tim sesuai ketentuan penyedia.', TRUE)
ON CONFLICT (id) DO NOTHING;

-- ============================================================
-- Produk Slot Langganan
-- ============================================================
INSERT INTO products (id, category_id, name, slug, description, active) VALUES
    ('0192f0a3-0000-0000-0000-000000000001', '0192f0a2-0000-0000-0000-000000000001', 'Netflix Premium - Slot Anggota Rumah Tangga', 'netflix-premium', 'Slot profil untuk anggota satu rumah tangga yang berbagi biaya langganan agar lebih hemat, sesuai ketentuan Netflix.', TRUE),
    ('0192f0a3-0000-0000-0000-000000000002', '0192f0a2-0000-0000-0000-000000000001', 'Disney+ Standard - Slot Anggota Rumah Tangga', 'disney-plus-standard', 'Slot profil untuk anggota satu rumah tangga yang berbagi biaya langganan agar lebih hemat, sesuai ketentuan Disney+.', TRUE),
    ('0192f0a3-0000-0000-0000-000000000003', '0192f0a2-0000-0000-0000-000000000002', 'Spotify Premium Family - Slot Anggota', 'spotify-premium-individual', 'Slot Premium Family untuk anggota keluarga yang tinggal serumah agar biaya lebih hemat, melalui undangan resmi sesuai ketentuan Spotify.', TRUE),
    ('0192f0a3-0000-0000-0000-000000000004', '0192f0a2-0000-0000-0000-000000000002', 'Apple Music Family - Slot Anggota', 'apple-music-individual', 'Slot anggota keluarga melalui Family Sharing agar biaya langganan lebih hemat, sesuai ketentuan Apple.', TRUE),
    ('0192f0a3-0000-0000-0000-000000000005', '0192f0a2-0000-0000-0000-000000000003', 'Microsoft 365 Family - Slot Anggota', 'microsoft-365-personal', 'Slot anggota paket Family dengan akses pribadi melalui undangan resmi agar lebih hemat, sesuai ketentuan Microsoft.', TRUE),
    ('0192f0a3-0000-0000-0000-000000000006', '0192f0a2-0000-0000-0000-000000000003', 'Canva Teams - Slot Anggota', 'canva-pro', 'Slot anggota tim melalui undangan resmi agar biaya kolaborasi lebih hemat, sesuai ketentuan Canva.', TRUE),
    ('0192f0a3-0000-0000-0000-000000000007', '0192f0a2-0000-0000-0000-000000000004', 'Google One 2 TB - Slot Keluarga', 'google-one-2tb', 'Slot anggota grup keluarga untuk berbagi manfaat penyimpanan secara lebih hemat, sesuai ketentuan Google.', TRUE),
    ('0192f0a3-0000-0000-0000-000000000008', '0192f0a2-0000-0000-0000-000000000004', 'Dropbox Family - Slot Anggota', 'dropbox-plus', 'Slot anggota paket Family dengan ruang pribadi melalui undangan resmi agar lebih hemat, sesuai ketentuan Dropbox.', TRUE)
ON CONFLICT (id) DO NOTHING;

-- ============================================================
-- Product Prices (amounts in IDR)
-- ============================================================
INSERT INTO product_prices (id, product_id, duration_id, amount, currency, active) VALUES
    -- Netflix Premium
    ('0192f0a4-0000-0000-0000-000000000001', '0192f0a3-0000-0000-0000-000000000001', '0192f0a1-0000-0000-0000-000000000001', 186000, 'IDR', TRUE),
    ('0192f0a4-0000-0000-0000-000000000002', '0192f0a3-0000-0000-0000-000000000001', '0192f0a1-0000-0000-0000-000000000002', 500000, 'IDR', TRUE),
    ('0192f0a4-0000-0000-0000-000000000003', '0192f0a3-0000-0000-0000-000000000001', '0192f0a1-0000-0000-0000-000000000004', 1900000, 'IDR', TRUE),
    -- Disney+ Standard
    ('0192f0a4-0000-0000-0000-000000000004', '0192f0a3-0000-0000-0000-000000000002', '0192f0a1-0000-0000-0000-000000000001', 139000, 'IDR', TRUE),
    ('0192f0a4-0000-0000-0000-000000000005', '0192f0a3-0000-0000-0000-000000000002', '0192f0a1-0000-0000-0000-000000000004', 1390000, 'IDR', TRUE),
    -- Spotify Premium Individual
    ('0192f0a4-0000-0000-0000-000000000006', '0192f0a3-0000-0000-0000-000000000003', '0192f0a1-0000-0000-0000-000000000001', 59000, 'IDR', TRUE),
    ('0192f0a4-0000-0000-0000-000000000007', '0192f0a3-0000-0000-0000-000000000003', '0192f0a1-0000-0000-0000-000000000002', 159000, 'IDR', TRUE),
    ('0192f0a4-0000-0000-0000-000000000008', '0192f0a3-0000-0000-0000-000000000003', '0192f0a1-0000-0000-0000-000000000004', 590000, 'IDR', TRUE),
    -- Apple Music Individual
    ('0192f0a4-0000-0000-0000-000000000009', '0192f0a3-0000-0000-0000-000000000004', '0192f0a1-0000-0000-0000-000000000001', 59000, 'IDR', TRUE),
    ('0192f0a4-0000-0000-0000-000000000010', '0192f0a3-0000-0000-0000-000000000004', '0192f0a1-0000-0000-0000-000000000004', 590000, 'IDR', TRUE),
    -- Microsoft 365 Personal
    ('0192f0a4-0000-0000-0000-000000000011', '0192f0a3-0000-0000-0000-000000000005', '0192f0a1-0000-0000-0000-000000000001', 129000, 'IDR', TRUE),
    ('0192f0a4-0000-0000-0000-000000000012', '0192f0a3-0000-0000-0000-000000000005', '0192f0a1-0000-0000-0000-000000000004', 1290000, 'IDR', TRUE),
    -- Canva Pro
    ('0192f0a4-0000-0000-0000-000000000013', '0192f0a3-0000-0000-0000-000000000006', '0192f0a1-0000-0000-0000-000000000001', 159000, 'IDR', TRUE),
    ('0192f0a4-0000-0000-0000-000000000014', '0192f0a3-0000-0000-0000-000000000006', '0192f0a1-0000-0000-0000-000000000004', 1590000, 'IDR', TRUE),
    -- Google One 2TB
    ('0192f0a4-0000-0000-0000-000000000015', '0192f0a3-0000-0000-0000-000000000007', '0192f0a1-0000-0000-0000-000000000001', 149000, 'IDR', TRUE),
    ('0192f0a4-0000-0000-0000-000000000016', '0192f0a3-0000-0000-0000-000000000007', '0192f0a1-0000-0000-0000-000000000004', 1490000, 'IDR', TRUE),
    -- Dropbox Plus
    ('0192f0a4-0000-0000-0000-000000000017', '0192f0a3-0000-0000-0000-000000000008', '0192f0a1-0000-0000-0000-000000000001', 179000, 'IDR', TRUE),
    ('0192f0a4-0000-0000-0000-000000000018', '0192f0a3-0000-0000-0000-000000000008', '0192f0a1-0000-0000-0000-000000000004', 1790000, 'IDR', TRUE)
ON CONFLICT (id) DO NOTHING;

-- ============================================================
-- Vouchers (dates relative to NOW, so they work whenever seeded)
-- ============================================================
INSERT INTO vouchers (id, code, type, value, min_order, max_discount, usage_limit, per_user_limit, starts_at, ends_at, active) VALUES
    ('0192f0a5-0000-0000-0000-000000000001', 'WELCOME10', 'percentage', 10, 50000, 50000, 100, 1, NOW() - INTERVAL '1 day', NOW() + INTERVAL '30 days', TRUE),
    ('0192f0a5-0000-0000-0000-000000000002', 'SAVE20K', 'fixed', 20000, 100000, 0, 50, 1, NOW() - INTERVAL '1 day', NOW() + INTERVAL '30 days', TRUE),
    ('0192f0a5-0000-0000-0000-000000000003', 'STREAM25', 'percentage', 25, 200000, 75000, 200, 1, NOW() - INTERVAL '1 day', NOW() + INTERVAL '60 days', TRUE),
    ('0192f0a5-0000-0000-0000-000000000004', 'EXPIRED', 'fixed', 10000, 50000, 0, 10, 1, NOW() - INTERVAL '60 days', NOW() - INTERVAL '1 day', FALSE)
ON CONFLICT (id) DO NOTHING;

-- ============================================================
-- Demo Admin User (password must be set via environment or admin API)
-- Password hash is a placeholder - DO NOT USE IN PRODUCTION
-- Run: make hash-password PASSWORD='YourSecurePass123!' to generate
-- ============================================================
-- INSERT INTO users (id, email, name, password_hash, role, email_verified_at, created_at, updated_at) VALUES
--     ('0192f0a6-0000-0000-0000-000000000001', 'admin@necly.example', 'Admin User', '$2a$04$PLACEHOLDER_HASH_REPLACE_VIA_ENV', 'admin', NOW(), NOW(), NOW())
-- ON CONFLICT (id) DO NOTHING;

-- ============================================================
-- Demo Customer User (password must be set via environment or registration)
-- ============================================================
-- INSERT INTO users (id, email, name, password_hash, role, email_verified_at, created_at, updated_at) VALUES
--     ('0192f0a6-0000-0000-0000-000000000002', 'customer@necly.example', 'Demo Customer', '$2a$04$PLACEHOLDER_HASH_REPLACE_VIA_ENV', 'customer', NOW(), NOW(), NOW())
-- ON CONFLICT (id) DO NOTHING;