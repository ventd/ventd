#!/usr/bin/env bash
# Gate 1: config survives upgrade
#
# Checks:
#   a. /etc/ventd/config.yaml is present and non-empty after upgrade.
#   b. The file is parseable YAML.
#   c. hwmon.dynamic_rebind is unchanged:
#        - if the fixture plants it as "true", it must still be "true"
#        - if it was absent, it must still be absent
#
# Environment (set by inner-test.sh before calling this script):
#   VENTD_ETC_DIR   path to ventd config directory (default /etc/ventd)
#   FIXTURE_HAS_DYNAMIC_REBIND  "true" if the fixture planted dynamic_rebind=true

set -euo pipefail

GATE="gate1-config"
VENTD_ETC_DIR="${VENTD_ETC_DIR:-/etc/ventd}"
FIXTURE_HAS_DYNAMIC_REBIND="${FIXTURE_HAS_DYNAMIC_REBIND:-true}"
CONFIG="$VENTD_ETC_DIR/config.yaml"

pass() { printf '[PASS] %s: %s\n' "$GATE" "$*"; }
fail() { printf '[FAIL] %s: %s\n' "$GATE" "$*"; exit 1; }

# a. File must exist and be non-empty.
[[ -f "$CONFIG" ]] || fail "config.yaml missing at $CONFIG"
[[ -s "$CONFIG" ]] || fail "config.yaml is empty at $CONFIG"

# b. Must be parseable YAML.
python3 - "$CONFIG" <<'PY'
import sys, yaml
try:
    with open(sys.argv[1]) as f:
        yaml.safe_load(f)
except Exception as e:
    print(f"YAML parse error: {e}", file=sys.stderr)
    sys.exit(1)
PY
# shellcheck disable=SC2181
if [[ $? -ne 0 ]]; then
    fail "config.yaml failed YAML parse"
fi

# c. Check hwmon.dynamic_rebind preservation.
if [[ "$FIXTURE_HAS_DYNAMIC_REBIND" == "true" ]]; then
    # Fixture planted dynamic_rebind: true; it must still be true post-upgrade.
    actual=$(python3 - "$CONFIG" <<'PY'
import sys, yaml
with open(sys.argv[1]) as f:
    cfg = yaml.safe_load(f)
hwmon = cfg.get("hwmon", {}) or {}
print(str(hwmon.get("dynamic_rebind", False)).lower())
PY
)
    if [[ "$actual" != "true" ]]; then
        fail "hwmon.dynamic_rebind was planted as true but is now: $actual"
    fi
else
    # Fixture did not plant dynamic_rebind; it must still be absent (or false/null).
    actual=$(python3 - "$CONFIG" <<'PY'
import sys, yaml
with open(sys.argv[1]) as f:
    cfg = yaml.safe_load(f)
hwmon = cfg.get("hwmon") or {}
# "absent" = key not present OR value is falsy (False, None, 0)
val = hwmon.get("dynamic_rebind")
print("absent" if not val else "present")
PY
)
    if [[ "$actual" != "absent" ]]; then
        fail "hwmon.dynamic_rebind was absent in fixture but is now present/true"
    fi
fi

pass "config.yaml present, valid YAML, hwmon.dynamic_rebind preserved"
