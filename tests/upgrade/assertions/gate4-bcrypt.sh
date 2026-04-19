#!/usr/bin/env bash
# Gate 4: admin bcrypt hash survives upgrade byte-identical
#
# Reads web.password_hash from /etc/ventd/config.yaml and compares it
# against the hash that was planted before v0.2.0 started (saved by
# inner-test.sh to EXPECTED_HASH_FILE).
#
# Environment:
#   VENTD_ETC_DIR       path to ventd config directory (default /etc/ventd)
#   EXPECTED_HASH_FILE  path to file containing the expected bcrypt hash

set -euo pipefail

GATE="gate4-bcrypt"
VENTD_ETC_DIR="${VENTD_ETC_DIR:-/etc/ventd}"
EXPECTED_HASH_FILE="${EXPECTED_HASH_FILE:-/tmp/ventd-upgrade-expected-hash.txt}"
CONFIG="$VENTD_ETC_DIR/config.yaml"

pass() { printf '[PASS] %s: %s\n' "$GATE" "$*"; }
fail() { printf '[FAIL] %s: %s\n' "$GATE" "$*"; exit 1; }

[[ -f "$EXPECTED_HASH_FILE" ]] || fail "expected hash file not found: $EXPECTED_HASH_FILE"
[[ -s "$EXPECTED_HASH_FILE" ]] || fail "expected hash file is empty: $EXPECTED_HASH_FILE"
[[ -f "$CONFIG" ]]              || fail "config.yaml not found at $CONFIG"

expected_hash=$(cat "$EXPECTED_HASH_FILE")

actual_hash=$(python3 - "$CONFIG" <<'PY'
import sys, yaml
with open(sys.argv[1]) as f:
    cfg = yaml.safe_load(f)
web = cfg.get("web") or {}
print(web.get("password_hash", ""))
PY
)

if [[ -z "$actual_hash" ]]; then
    fail "web.password_hash is empty or absent in config.yaml post-upgrade"
fi

if [[ "$actual_hash" != "$expected_hash" ]]; then
    fail "bcrypt hash changed post-upgrade (expected: ${expected_hash:0:20}... got: ${actual_hash:0:20}...)"
fi

pass "admin bcrypt hash is byte-identical pre/post upgrade"
