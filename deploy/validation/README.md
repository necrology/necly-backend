# W1 isolated validation environment

This directory is the repeatable, disposable validation contract for the W0
baseline. It is **not** a production deployment file and must not be combined
with the production `.env.production`, production volumes or the `necly`
Compose project.

## Isolation contract

| Boundary | Validation value | Production value (unchanged) |
|---|---|---|
| Compose project | `necly-validation` | `necly` |
| PostgreSQL | `postgres:16-alpine`, volume `necly-validation_postgres_data`, `127.0.0.1:55432` | `necly_necly_postgres_data`, internal only |
| Redis | `redis:7-alpine`, volume `necly-validation_redis_data`, `127.0.0.1:56379`, password required | `necly_necly_redis_data` |
| API | local image from the pinned source SHA, `127.0.0.1:58080` | `necly-api:latest` |
| Frontend | local image from the pinned source SHA, `127.0.0.1:53000` | `necly-frontend:latest` |
| Edge | nginx with a disposable self-signed cert, `127.0.0.1:58088` -> `127.0.0.1:58443` | Cloudflare tunnel |
| Runtime network | `necly-validation`, `internal: true` | `necly_default` |
| Payment | `PAYMENT_PROVIDER=mock`, `PAYMENT_SANDBOX=true` | unverified (Q2); never changed here |
| Secrets | generated into `.runtime.env`, mode `0600` | `.env.production`, never read here |
| Data | explicitly applied W0 migrations plus optional synthetic `002` seed | existing production data, never mounted here |

The runtime network is `internal: true`. The API cannot reach a real payment
provider or any other external host from this stack. Build-time base image and
npm downloads use the Docker builder network, not the runtime network.

## Commands

```sh
# Start; builds labels from the current source SHA and runs smoke checks.
./scripts/up.sh

# Restore rehearsal: fresh pg_dump -> separate PostgreSQL volume -> counts + API smoke.
./scripts/backup-restore.sh

# Prior-image rollback rehearsal with measured /ready time.
./scripts/rollback.sh

# Checksum-mismatch and failing-SQL abort proof.
./scripts/failed-migration.sh

# Stop and delete only the disposable validation project.
./scripts/down.sh
```

Evidence logs are written to `deploy/validation/evidence/` and are excluded
from git. Each log records the source SHA, counts, checks and measured times.
`up.sh` runs `002_seed_data.sql` only when `VALIDATION_SEED=1` (the default for
a disposable smoke stack). Set `VALIDATION_SEED=0` to prove an empty schema.

## What is intentionally not here

- No production secrets, production data, production volume or production
  project name.
- No live payment provider, real charge, live callback or adversarial test.
- No host-wide firewall, Cloudflare, tunnel or reverse-proxy change.
- No destructive migration or seed deletion.
- No replacement for the W2 versioned migration runner. `migrate.sh` is an
  explicit W1 allow-list with W0 checksums; W2 replaces the production path.

## Access and evidence limits

The host's external TLS behavior is checked read-only from the public edge.
The Cloudflare tunnel configuration (`/etc/cloudflared/config.yml`) is not
readable by the `servermaul` operator account. That is recorded as an access
limit, not as proof that the setting is absent. Any edge change remains a W11
step with its own approval.
