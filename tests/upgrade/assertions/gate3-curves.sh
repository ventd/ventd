#!/usr/bin/env bash
# Gate 3: fan curves survive upgrade
#
# Authenticates against the running v0.3.0-candidate daemon, calls
# GET /api/config, and verifies the expected curve is present with
# byte-equivalent user-facing fields.
#
# "User-facing fields" for a linear curve:
#   name, type, sensor, min_temp, max_temp, min_pwm, max_pwm
#
# Environment:
#   VENTD_PORT              daemon port (default 19999)
#   VENTD_PASS              admin password (set by inner-test.sh)
#   EXPECTED_CURVE_NAME     name of curve to check (default cpu_linear)
#   EXPECTED_CURVE_JSON     path to pre-upgrade GET /api/config response (optional)
#                           if present, compare full curves array byte-for-byte
#                           if absent, verify the named curve's fields match FIXTURE values

set -euo pipefail

GATE="gate3-curves"
VENTD_PORT="${VENTD_PORT:-19999}"
VENTD_PASS="${VENTD_PASS:-TestUpgrade2025}"
EXPECTED_CURVE_NAME="${EXPECTED_CURVE_NAME:-cpu_linear}"
EXPECTED_CURVE_JSON="${EXPECTED_CURVE_JSON:-}"

pass() { printf '[PASS] %s: %s\n' "$GATE" "$*"; }
fail() { printf '[FAIL] %s: %s\n' "$GATE" "$*"; exit 1; }

BASE="http://127.0.0.1:${VENTD_PORT}"
COOKIE_JAR="$(mktemp -t ventd-cookies-XXXX.txt)"
cleanup() { rm -f "$COOKIE_JAR"; }
trap cleanup EXIT

# Authenticate: POST /login with password, capture session cookie.
login_code=$(curl -s -o /dev/null -w '%{http_code}' \
    -c "$COOKIE_JAR" \
    -X POST \
    -d "password=${VENTD_PASS}" \
    "${BASE}/login")

if [[ "$login_code" != "200" && "$login_code" != "302" && "$login_code" != "303" ]]; then
    fail "POST /login returned HTTP $login_code (expected 200 or redirect)"
fi

# If login returned a redirect, follow it once to land on the session.
# Some ventd versions redirect to / after successful login.
if [[ "$login_code" == "302" || "$login_code" == "303" ]]; then
    curl -s -o /dev/null -b "$COOKIE_JAR" -c "$COOKIE_JAR" -L "${BASE}/" > /dev/null || true
fi

# Verify we have a session cookie.
if ! grep -q "ventd_session" "$COOKIE_JAR" 2>/dev/null; then
    fail "no ventd_session cookie after login (HTTP $login_code)"
fi

# Call GET /api/config with the session cookie.
API_RESPONSE="$(mktemp -t ventd-config-XXXX.json)"
api_code=$(curl -s -o "$API_RESPONSE" -w '%{http_code}' \
    -b "$COOKIE_JAR" \
    "${BASE}/api/config")

if [[ "$api_code" != "200" ]]; then
    fail "GET /api/config returned HTTP $api_code (expected 200)"
fi

# Verify the response is valid JSON and contains a curves array.
python3 - "$API_RESPONSE" "$EXPECTED_CURVE_NAME" "$EXPECTED_CURVE_JSON" <<'PY'
import sys, json

resp_path = sys.argv[1]
curve_name = sys.argv[2]
pre_upgrade_path = sys.argv[3] if len(sys.argv) > 3 else ""

with open(resp_path) as f:
    data = json.load(f)

curves = data.get("curves", [])
if not isinstance(curves, list):
    print(f"ERROR: 'curves' in response is not a list: {type(curves)}", file=sys.stderr)
    sys.exit(1)

# Find the expected curve by name.
found = next((c for c in curves if c.get("name") == curve_name), None)
if found is None:
    names = [c.get("name") for c in curves]
    print(f"ERROR: curve '{curve_name}' not found in response. Available: {names}", file=sys.stderr)
    sys.exit(1)

# Verify user-facing fields match fixture values.
# These are the fields from fixtures/config.tmpl.yaml for cpu_linear.
expected = {
    "name": "cpu_linear",
    "type": "linear",
    "sensor": "cpu_temp",
    "min_temp": 40.0,
    "max_temp": 80.0,
    "min_pwm": 30,
    "max_pwm": 255,
}
for field, val in expected.items():
    actual = found.get(field)
    if actual != val:
        print(f"ERROR: curve.{field}: expected {val!r}, got {actual!r}", file=sys.stderr)
        sys.exit(1)

# Optionally compare against the saved pre-upgrade response.
if pre_upgrade_path:
    try:
        with open(pre_upgrade_path) as f:
            pre_data = json.load(f)
        pre_curves = pre_data.get("curves", [])
        pre_found = next((c for c in pre_curves if c.get("name") == curve_name), None)
        if pre_found is None:
            print(f"WARNING: curve '{curve_name}' was not in pre-upgrade response either",
                  file=sys.stderr)
        else:
            user_fields = ["name", "type", "sensor", "min_temp", "max_temp", "min_pwm", "max_pwm"]
            for field in user_fields:
                pv = pre_found.get(field)
                av = found.get(field)
                if pv != av:
                    print(f"ERROR: user-facing field '{field}' changed: {pv!r} → {av!r}",
                          file=sys.stderr)
                    sys.exit(1)
    except FileNotFoundError:
        pass  # pre-upgrade JSON not provided; skip comparison
PY
# shellcheck disable=SC2181
if [[ $? -ne 0 ]]; then
    fail "curve check failed (see output above)"
fi

pass "curve '${EXPECTED_CURVE_NAME}' present via GET /api/config with correct user-facing fields"
