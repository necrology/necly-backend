# C14 backup, restore, rollback and edge-change runbook

Status: prepared by DevOps/SRE in W1. This runbook is **not** authorization to
touch production. W11 executes it only after the Tech Lead approves the exact
PRs and image digests, protected-branch approvals are satisfied, and a fresh
backup has been reviewed.

## 0. Preconditions

- Exact release manifest reviewed: frontend/backend source SHAs, registry
  manifest digests, migration list/checksums, build args and lockfile hashes.
- Isolated restore rehearsal passed (`scripts/backup-restore.sh` evidence).
- Isolated rollback rehearsal passed (`scripts/rollback.sh` evidence).
- Current production image and database state recorded read-only.
- No destructive migration, data cleanup, blind down migration or live charge
  is in scope.
- Edge change (HTTPS redirect/HSTS) has its own scoped approval. Do not change
  host-wide firewall rules or unrelated workloads.

## 1. Preflight (read-only)

Record, without printing secrets:

```sh
docker compose -p necly ps
docker inspect necly-api-1 necly-frontend-1 \
  --format '{{.Name}} {{.Image}} {{.Config.Image}}'
docker image inspect necly-api:latest necly-frontend:latest \
  --format '{{.Id}} {{json .Config.Labels}}'
curl -sS -D - -o /dev/null https://api.necly-service.my.id/ready
curl -sS -D - -o /dev/null https://necly-service.my.id/
```

Stop if the current production digest/tag is not the one in the release
manifest, the readiness check is not 200, or the public catalog/price check is
inconsistent.

## 2. Fresh backup before deployment

Take a fresh logical backup immediately before the release. Store it outside
the application volume, record its SHA-256 and size, and do not overwrite the
previous approved backup.

```sh
STAMP=$(date -u +%Y%m%dT%H%M%SZ)
docker compose -p necly exec -T postgres \
  pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc > "necly-$STAMP.dump"
sha256sum "necly-$STAMP.dump"
```

The dump must complete without error. A backup that is missing, truncated or
has an unexpected checksum blocks deployment.

## 3. Restore into isolation (must already pass)

W1 proved this on disposable data with `deploy/validation/scripts/backup-restore.sh`:

1. fresh `pg_dump -Fc` from the validation PostgreSQL 16;
2. restore into a **separate** PostgreSQL volume/database;
3. compare row counts for all 23 public tables;
4. start an API against the restored database and verify `/ready` and public
   catalog shapes;
5. record the measured duration.

For production, the restore target is an isolated database/volume, never the
live database. Production data is not copied into the validation environment.

## 4. Migration gate

- Apply only the reviewed additive/compatible migration list.
- Verify every file checksum against the ledger/baseline before DDL.
- Acquire the runner's advisory lock; one migrator at a time.
- If the migration exits nonzero, **abort the deployment**. Do not start the
  new application image. Roll back that file's transaction and leave later
  versions unapplied.
- A second run must be a no-op. Never replay `002_seed_data.sql` as part of the
  production chain.
- No blind down migration. Rollback keeps additive columns/tables.

W1 proved checksum rejection and failing-SQL rollback in isolation with
`deploy/validation/scripts/failed-migration.sh`. W2 replaces the production
path with the versioned runner and ledger.

## 5. Application rollout and rollback

Deploy only the affected NEC LY services using registry images referenced by
`@sha256` digest. Do not rebuild on the host and do not use a mutable `:latest`
tag for a release.

Record the candidate and prior digests before rollout. Rehearsal evidence from
W1 (local image IDs, not a registry digest):

| Step | Evidence |
|---|---|
| Candidate `/ready` | measured in isolated rollback rehearsal |
| Rollback to prior image | measured in isolated rollback rehearsal |
| Post-rollback `/ready` | `{"status":"ready"}` |

On failed readiness, elevated errors, wrong prices or broken auth:

1. switch the affected application service(s) back to the prior **digest**;
2. keep the additive database columns/tables;
3. do not automatically restore over live data or run a database down
   migration;
4. record timings, digest, symptoms and rollback outcome on the release task.

## 6. Edge change: HTTPS redirect and HSTS

W1 external read-only evidence on 2026-10-18 found:

- `http://necly-service.my.id/` returns `200`, not a redirect;
- `https://necly-service.my.id/` has no `Strict-Transport-Security` header;
- `http://api.necly-service.my.id/health` returns `200`, not a redirect;
- `https://api.necly-service.my.id/health` and `/ready` return `200` and have
  no HSTS header.

The origin/tunnel configuration file is not readable by the `servermaul`
operator account, so origin settings cannot be asserted from this account.
External absence is evidence of public behavior, not proof about unread origin
configuration.

Approved W11 edge change, if authorized:

1. enable an HTTP-to-HTTPS redirect at the public edge for both hostnames;
2. send `Strict-Transport-Security` on HTTPS responses with a reviewed
   `max-age`; add `includeSubDomains` only after every subdomain is HTTPS;
3. do not submit to the HSTS preload list in this release;
4. verify in isolation first (W1 edge: `301` to `https://...:58443` and HSTS
   present), then verify production with `curl` using both schemes;
5. roll back the edge change by restoring the prior reviewed edge
   configuration if redirect loops, certificate errors or API/frontend
   breakage appear.

This is a public edge/tunnel change, not a host-wide firewall change. It
requires the exact edge configuration and approval in W11.

## 7. Go/no-go record

- [ ] exact PRs and digests approved by Tech Lead
- [ ] protected-branch approvals satisfied
- [ ] fresh backup checksum recorded
- [ ] restore rehearsal passed
- [ ] rollback rehearsal passed with measured duration
- [ ] migration checksums verified; failure abort proven
- [ ] `PAYMENT_PROVIDER`/sandbox mode attested (names/values policy only)
- [ ] readiness `/ready` 200 before and after
- [ ] public catalog/price consistency checked read-only
- [ ] HTTPS redirect and HSTS verified
- [ ] no live charge, production data cleanup or destructive migration
- [ ] deployed digests, timings and remaining limitations recorded
