-- Necly Services - Development Seed Data
-- Run this AFTER 001_initial_schema.sql on a DEVELOPMENT database ONLY.
-- DO NOT RUN IN PRODUCTION. This file contains no real credentials or secrets.
-- Inventory accounts with encrypted credentials must be created via the admin API
-- using the CREDENTIAL_ENCRYPTION_KEY from environment.

-- ============================================================
-- Product Durations
-- ============================================================
INSERT INTO product_durations (id, name, days) VALUES
    ('0192f0a1-0000-0000-0000-000000000001', 'Monthly', 30),
    ('0192f0a1-0000-0000-0000-000000000002', 'Quarterly', 90),
    ('0192f0a1-0000-0000-0000-000000000003', 'Semi-Annual', 180),
    ('0192f0a1-0000-0000-0000-000000000004', 'Annual', 365)
ON CONFLICT (id) DO NOTHING;

-- ============================================================
-- Categories - Streaming & Productivity Services
-- ============================================================
INSERT INTO categories (id, name, slug, description, active) VALUES
    ('0192f0a2-0000-0000-0000-000000000001', 'Video Streaming', 'video-streaming', 'Premium video streaming subscriptions', TRUE),
    ('0192f0a2-0000-0000-0000-000000000002', 'Music Streaming', 'music-streaming', 'High-quality music streaming plans', TRUE),
    ('0192f0a2-0000-0000-0000-000000000003', 'Productivity Suites', 'productivity-suites', 'Office and collaboration software subscriptions', TRUE),
    ('0192f0a2-0000-0000-0000-000000000004', 'Cloud Storage', 'cloud-storage', 'Personal and team cloud storage plans', TRUE)
ON CONFLICT (id) DO NOTHING;

-- ============================================================
-- Products
-- ============================================================
INSERT INTO products (id, category_id, name, slug, description, active) VALUES
    ('0192f0a3-0000-0000-0000-000000000001', '0192f0a2-0000-0000-0000-000000000001', 'Netflix Premium', 'netflix-premium', '4K HDR streaming on up to 4 devices', TRUE),
    ('0192f0a3-0000-0000-0000-000000000002', '0192f0a2-0000-0000-0000-000000000001', 'Disney+ Standard', 'disney-plus-standard', 'HD streaming on 2 devices', TRUE),
    ('0192f0a3-0000-0000-0000-000000000003', '0192f0a2-0000-0000-0000-000000000002', 'Spotify Premium Individual', 'spotify-premium-individual', 'Ad-free music, offline listening, high quality', TRUE),
    ('0192f0a3-0000-0000-0000-000000000004', '0192f0a2-0000-0000-0000-000000000002', 'Apple Music Individual', 'apple-music-individual', 'Lossless audio, spatial audio, offline', TRUE),
    ('0192f0a3-0000-0000-0000-000000000005', '0192f0a2-0000-0000-0000-000000000003', 'Microsoft 365 Personal', 'microsoft-365-personal', 'Office apps, 1TB OneDrive, premium features', TRUE),
    ('0192f0a3-0000-0000-0000-000000000006', '0192f0a2-0000-0000-0000-000000000003', 'Canva Pro', 'canva-pro', 'Premium templates, brand kit, background remover', TRUE),
    ('0192f0a3-0000-0000-0000-000000000007', '0192f0a2-0000-0000-0000-000000000004', 'Google One 2TB', 'google-one-2tb', '2TB storage across Drive, Gmail, Photos', TRUE),
    ('0192f0a3-0000-0000-0000-000000000008', '0192f0a2-0000-0000-0000-000000000004', 'Dropbox Plus', 'dropbox-plus', '2TB storage, smart sync, file recovery', TRUE)
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