#!/usr/bin/env bash
# Smoke checks for the isolated environment. Writes a timestamped evidence log.
set -eu
ROOT=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
cd "$ROOT"
# shellcheck disable=SC1091
. ./scripts/lib.sh
load_runtime_env
mkdir -p evidence
LOG="evidence/smoke-$(timestamp).log"
exec > >(tee "$LOG") 2>&1

fail() { echo "FAIL: $*" >&2; exit 1; }
pass() { echo "PASS: $*"; }

echo "# W1 isolated validation smoke ($(timestamp))"
echo "source_sha=$SOURCE_SHA"
echo "project=necly-validation"

EDGE_RESOLVE=(--resolve validation.localhost:58443:127.0.0.1)
API_RESOLVE=(--resolve api.validation.localhost:58443:127.0.0.1)
API_EDGE=https://api.validation.localhost:58443

code=$(curl -ksS -o /tmp/w1-health.json -w '%{http_code}' --max-time 5 "${API_RESOLVE[@]}" "$API_EDGE/health")
[ "$code" = "200" ] || fail "API /health returned $code"
grep -q '"status":"ok"' /tmp/w1-health.json || fail "API /health body is not ok"
pass "API /health 200"

code=$(curl -ksS -o /tmp/w1-ready.json -w '%{http_code}' --max-time 5 "${API_RESOLVE[@]}" "$API_EDGE/ready")
[ "$code" = "200" ] || fail "API /ready returned $code"
grep -q '"status":"ready"' /tmp/w1-ready.json || fail "API /ready body is not ready"
pass "API /ready 200 (PostgreSQL + Redis reachable)"

curl -kfsS --max-time 5 "${API_RESOLVE[@]}" "$API_EDGE/api/v1/public/categories" -o /tmp/w1-categories.json
jq -e '.categories | type == "array"' /tmp/w1-categories.json >/dev/null || fail "public categories shape"
pass "public categories contract shape ($(jq '.categories | length' /tmp/w1-categories.json) rows)"

curl -kfsS --max-time 5 "${API_RESOLVE[@]}" "$API_EDGE/api/v1/public/products" -o /tmp/w1-products.json
jq -e '.products | type == "array"' /tmp/w1-products.json >/dev/null || fail "public products shape"
pass "public products contract shape ($(jq '.products | length' /tmp/w1-products.json) rows)"

curl -kfsS --max-time 5 "${EDGE_RESOLVE[@]}" https://validation.localhost:58443/ -o /tmp/w1-home.html
grep -qi 'necly' /tmp/w1-home.html || fail "frontend home does not contain Necly"
pass "frontend / 200 with live-mode build"

compose exec -T api wget -qO- http://127.0.0.1:8080/health >/tmp/w1-direct-health.json
grep -q '"status":"ok"' /tmp/w1-direct-health.json || fail "internal API health"
pass "internal API health reachable without a published host port"

http_code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 http://127.0.0.1:58088/ -H 'Host: validation.localhost')
[ "$http_code" = "301" ] || fail "edge HTTP redirect returned $http_code, expected 301"
location=$(curl -sS -D - -o /dev/null --max-time 5 http://127.0.0.1:58088/ -H 'Host: validation.localhost' | tr -d '\r' | awk 'tolower($1)=="location:" {print $2}')
case "$location" in
  https://validation.localhost:58443/*) ;;
  *) fail "edge HTTP redirect location is $location" ;;
esac
pass "edge HTTP -> HTTPS 301 ($location)"

https_code=$(curl -ksS -o /tmp/w1-edge-home.html -w '%{http_code}' --max-time 5 "${EDGE_RESOLVE[@]}" https://validation.localhost:58443/)
[ "$https_code" = "200" ] || fail "edge HTTPS frontend returned $https_code"
pass "edge HTTPS frontend 200"

hsts=$(curl -ksS -D - -o /dev/null --max-time 5 "${EDGE_RESOLVE[@]}" https://validation.localhost:58443/ | tr -d '\r' | awk 'tolower($1)=="strict-transport-security:" {print $2}')
case "$hsts" in
  max-age=*) pass "edge HTTPS sends HSTS ($hsts)" ;;
  *) fail "edge HTTPS missing HSTS header" ;;
esac

api_edge_code=$(curl -ksS -o /tmp/w1-edge-ready.json -w '%{http_code}' --max-time 5 "${API_RESOLVE[@]}" "$API_EDGE/ready")
[ "$api_edge_code" = "200" ] || fail "edge HTTPS API /ready returned $api_edge_code"
pass "edge HTTPS API readiness 200"

count() {
  compose exec -T postgres psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "SELECT count(*) FROM $1"
}
echo "row_counts:"
for table in users categories products product_prices inventory_slots orders order_items payments; do
  value=$(count "$table")
  echo "  $table=$value"
done
[ "$(count categories)" -ge 1 ] || fail "expected seeded categories in disposable database"
pass "database row counts captured"

redis_pong=$(compose exec -T redis redis-cli -a "$REDIS_PASSWORD" ping | tr -d '\r')
[ "$redis_pong" = "PONG" ] || fail "redis PING returned $redis_pong"
pass "redis PING"

provider=$(compose exec -T api printenv PAYMENT_PROVIDER | tr -d '\r')
sandbox=$(compose exec -T api printenv PAYMENT_SANDBOX | tr -d '\r')
[ "$provider" = "mock" ] || fail "PAYMENT_PROVIDER=$provider (expected mock)"
[ "$sandbox" = "true" ] || fail "PAYMENT_SANDBOX=$sandbox (expected true)"
pass "payment provider is mock/sandbox; no live charge path"

if compose exec -T api printenv DB_HOST | grep -q 'postgres'; then
  pass "API uses disposable postgres service"
else
  fail "API DB_HOST is not the validation service"
fi
echo "evidence_log=$LOG"
