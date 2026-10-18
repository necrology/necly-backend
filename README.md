# Necly Services Backend

Go Fiber clean/hexagonal backend for Necly Services — Indonesian digital subscription slot marketplace.

## Architecture

```
cmd/server         → entrypoint
internal/
  config           → env-based config with validation
  domain           → core types (User, Product, Order, etc.)
  handler          → Fiber routes, middleware, request/response
  middleware       → auth, rate-limit, CORS, logging, recover
  platform/database → GORM connection + AutoMigrate
  repository/gormrepo → repository implementations
  service          → business logic (auth, catalog, checkout, payments, vouchers, inventory)
pkg/               → shared utilities
migrations/        → SQL schema + seed data
```

## Stack

- Go 1.26
- Fiber v2
- GORM (PostgreSQL)
- Redis (go-redis/v9)
- JWT (golang-jwt/v5)
- bcrypt (golang.org/x/crypto)

## Quick Start (Docker Compose)

```bash
# From parent directory (C:\Source Next)
docker compose -f necly-compose.yml up --build
```

- API: http://localhost:8080
- Health: `GET /api/v1/health`
- Swagger (if enabled): `GET /api/v1/docs`

## Local Development (without Docker)

### Prerequisites

- PostgreSQL 16+
- Redis 7+
- MinIO/S3-compatible (optional, for assets)

### Environment

```bash
cp .env.example .env
# Edit .env with your values
```

### Run Migrations

```bash
go run ./cmd/migrate
# Or with flags:
go run ./cmd/migrate -host localhost -port 5432 -user postgres -password postgres -name necly
```

### Run Server

```bash
go run ./cmd/server
```

## API Endpoints (v1)

### Auth
- `POST /api/v1/auth/register` — register (email/password)
- `POST /api/v1/auth/login` — login (email/password)
- `POST /api/v1/auth/refresh` — refresh access token
- `POST /api/v1/auth/logout` — revoke refresh token
- `GET /api/v1/auth/me` — current user (auth required)
- `POST /api/v1/auth/verify-email` — verify email token
- `GET /api/v1/auth/google` — initiate Google OAuth
- `GET /api/v1/auth/google/callback` — Google OAuth callback

### Public Catalog
- `GET /api/v1/categories` — list categories
- `GET /api/v1/products` — list products (filter: category, search, page)
- `GET /api/v1/products/:slug` — product detail

### Cart (auth required)
- `GET /api/v1/cart` — get cart
- `POST /api/v1/cart/items` — add item
- `PATCH /api/v1/cart/items/:id` — update quantity
- `DELETE /api/v1/cart/items/:id` — remove item

### Checkout & Orders (auth required)
- `POST /api/v1/checkout` — create order (idempotency key required)
- `GET /api/v1/orders` — list my orders
- `GET /api/v1/orders/:id` — order detail
- `GET /api/v1/orders/:id/tracking` — order tracking/timeline

### Payments (auth required)
- `POST /api/v1/payments/:orderId/start` — initiate payment
- `POST /api/v1/webhook/midtrans` — Midtrans callback
- `POST /api/v1/webhook/xendit` — Xendit callback

### Admin/Seller (role required)
- `GET /api/v1/admin/products` — list products
- `POST /api/v1/admin/products` — create product
- `PATCH /api/v1/admin/products/:id` — update product
- `DELETE /api/v1/admin/products/:id` — soft delete
- `GET /api/v1/admin/inventory` — inventory accounts & slots
- `POST /api/v1/admin/inventory` — add inventory account
- `GET /api/v1/admin/orders` — all orders (filter by status)
- `PATCH /api/v1/admin/orders/:id/status` — transition order status
- `GET /api/v1/admin/payments` — payments
- `GET /api/v1/admin/vouchers` — vouchers CRUD
- `GET /api/v1/admin/users` — users
- `GET /api/v1/admin/testimonials` — testimonials moderation
- `GET /api/v1/admin/assets` — assets
- `GET /api/v1/admin/audit-logs` — audit logs

## Testing

```bash
go test ./... -count=1
go vet ./...
gofmt -l .
go build -o bin/necly-backend.exe ./cmd/server
```

## Key Design Decisions

- **Row-level locking**: `SELECT FOR UPDATE SKIP LOCKED` in checkout to prevent overselling
- **Idempotency**: Required on checkout; webhook events deduped by `provider_event_id`
- **Credential encryption**: AES-256-GCM per-record random nonces
- **Refresh tokens**: Opaque, SHA-256 hashed whitelist in DB, revocable, httpOnly cookie
- **Cookie path migration**: login, refresh and logout expire the legacy host-only `Path=/` refresh cookie (and a domain-scoped variant when configured), so a pre-upgrade cookie cannot survive logout
- **Access tokens**: Short-lived (15m) HS256 JWT bound to a refresh session (`sid`); account state, role and session revocation are re-checked on every authenticated request
- **Password changes**: Password update and revocation of other sessions run in one transaction. A change made with a validated access session (`sid`) keeps that session and revokes the rest; a service call with no session identity revokes every live session
- **Google OAuth**: State is bound to a browser nonce cookie (always SameSite=Lax so the cross-site callback carries it) and consumed atomically from Redis; missing Redis or an unusable state fails closed
- **Email**: In production a missing/TLS-less SMTP transport fails closed instead of logging activation or reset tokens
- **Rate limiting**: Per-IP on login/register; general API rate limit
- **Webhook verification**: Midtrans HMAC-SHA512; Xendit callback token
- **Soft deletes**: `deleted_at` on core entities
- **Audit logging**: All mutations

## Production Checklist

- [ ] Set `APP_ENV=production`
- [ ] Strong `JWT_SECRET` (32+ bytes)
- [ ] `CREDENTIAL_ENCRYPTION_KEY` (base64 32 bytes)
- [ ] `DB_SSL_MODE=require` + valid certs
- [ ] `COOKIE_SECURE=true`, `COOKIE_SAME_SITE=Lax`
- [ ] `PAYMENT_PROVIDER=midtrans|xendit` (not mock)
- [ ] Real `SMTP_*` for transactional email with `SMTP_TLS=true` (SMTP without STARTTLS is rejected; production registration/reset fail closed when mail is unavailable)
- [ ] `REDIS_REQUIRED=true`
- [ ] `S3_*` for asset storage
- [ ] `GOOGLE_*` for OAuth
- [ ] HTTPS `APP_BASE_URL`
- [ ] Healthchecks + monitoring
- [ ] Backup/restore plan