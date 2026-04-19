#!/usr/bin/env bash
# Gate 2: calibration file survives upgrade
#
# Checks:
#   a. /etc/ventd/calibration.json is present and non-empty after upgrade.
#   b. The file is parseable JSON.
#   c. schema_version field is present and equals the value planted by the fixture.
#
# Environment:
#   VENTD_ETC_DIR            path to ventd config directory (default /etc/ventd)
#   FIXTURE_SCHEMA_VERSION   expected schema_version (default 2)

set -euo pipefail

GATE="gate2-calibration"
VENTD_ETC_DIR="${VENTD_ETC_DIR:-/etc/ventd}"
FIXTURE_SCHEMA_VERSION="${FIXTURE_SCHEMA_VERSION:-2}"
CAL="$VENTD_ETC_DIR/calibration.json"

pass() { printf '[PASS] %s: %s\n' "$GATE" "$*"; }
fail() { printf '[FAIL] %s: %s\n' "$GATE" "$*"; exit 1; }

# a. File must exist and be non-empty.
[[ -f "$CAL" ]] || fail "calibration.json missing at $CAL"
[[ -s "$CAL" ]] || fail "calibration.json is empty at $CAL"

# b+c. Must be parseable JSON with correct schema_version.
python3 - "$CAL" "$FIXTURE_SCHEMA_VERSION" <<'PY'
import sys, json

path = sys.argv[1]
expected_schema = int(sys.argv[2])

try:
    with open(path) as f:
        data = json.load(f)
except json.JSONDecodeError as e:
    print(f"JSON parse error: {e}", file=sys.stderr)
    sys.exit(1)

sv = data.get("schema_version")
if sv is None:
    print("ERROR: schema_version field is missing", file=sys.stderr)
    sys.exit(1)

if int(sv) != expected_schema:
    print(f"ERROR: schema_version is {sv}, expected {expected_schema}", file=sys.stderr)
    sys.exit(1)

results = data.get("results", {})
if not isinstance(results, dict) or len(results) == 0:
    print("ERROR: results field is missing or empty", file=sys.stderr)
    sys.exit(1)
PY
# shellcheck disable=SC2181
if [[ $? -ne 0 ]]; then
    fail "calibration.json failed JSON/schema check"
fi

pass "calibration.json present, valid JSON, schema_version=$FIXTURE_SCHEMA_VERSION"
