-- ============================================================
-- 004: Issue reports (QA "Laporan Kendala")
--
-- Visitors file a report against an order number they already placed. The row
-- is stored even when no SMTP credentials are configured, so the admin can
-- still read every report from the database. Idempotent: safe to run on every
-- boot, including a fresh volume where initdb replays 001-00N in order.
-- ============================================================

CREATE TABLE IF NOT EXISTS issue_reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) NOT NULL,
    order_number VARCHAR(64) NOT NULL,
    category VARCHAR(64) NOT NULL,
    message TEXT NOT NULL,
    status VARCHAR(24) NOT NULL DEFAULT 'new',
    ip_address VARCHAR(45),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    handled_at TIMESTAMPTZ
);

-- The 3-hour cooldown fallback reads the newest report for an email.
CREATE INDEX IF NOT EXISTS idx_issue_reports_email_created_at ON issue_reports(email, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_issue_reports_order_number ON issue_reports(order_number);
CREATE INDEX IF NOT EXISTS idx_issue_reports_status ON issue_reports(status);
