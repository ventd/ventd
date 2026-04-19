#!/usr/bin/env bash
# Gate 5: no re-wizard redirect on first GET / after upgrade
#
# After upgrading from v0.2.0 to v0.3.0-candidate, the daemon must NOT
# redirect GET / to /setup or /wizard. A redirect to /login is acceptable
# (normal unauthenticated request). A 3xx to /setup or /wizard means the
# daemon incorrectly entered first-boot mode and is forcing re-configuration.
#
# Environment:
#   VENTD_PORT  daemon port (default 19999)

set -euo pipefail

GATE="gate5-no-wizard"
VENTD_PORT="${VENTD_PORT:-19999}"

pass() { printf '[PASS] %s: %s\n' "$GATE" "$*"; }
fail() { printf '[FAIL] %s: %s\n' "$GATE" "$*"; exit 1; }

BASE="http://127.0.0.1:${VENTD_PORT}"

# Follow at most one redirect so we land on the final destination.
# Use -D - to dump headers, then parse the Location header.
HEADERS="$(mktemp -t ventd-headers-XXXX.txt)"
cleanup() { rm -f "$HEADERS"; }
trap cleanup EXIT

http_code=$(curl -s -o /dev/null -D "$HEADERS" -w '%{http_code}' \
    --max-redirs 0 \
    "${BASE}/")

# If it's a redirect, check the Location header.
if [[ "$http_code" == "301" || "$http_code" == "302" || "$http_code" == "303" || "$http_code" == "307" || "$http_code" == "308" ]]; then
    location=$(grep -i '^location:' "$HEADERS" | tr -d '\r\n' | sed 's/^[Ll][Oo][Cc][Aa][Tt][Ii][Oo][Nn]: *//')
    # Normalise to lower-case path for comparison.
    loc_lower=$(echo "$location" | tr '[:upper:]' '[:lower:]')
    if echo "$loc_lower" | grep -qE '/setup|/wizard'; then
        fail "GET / redirected to wizard/setup: $location (HTTP $http_code)"
    fi
    # Redirect to /login is expected and acceptable.
    pass "GET / returned HTTP $http_code → $location (no wizard redirect)"
elif [[ "$http_code" == "200" ]]; then
    pass "GET / returned 200 (dashboard served directly)"
else
    fail "GET / returned unexpected HTTP $http_code"
fi
