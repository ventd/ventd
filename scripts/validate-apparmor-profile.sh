#!/usr/bin/env bash
# Validate the ventd AppArmor profile for syntax and required permissions.
# Run by CI (shellcheck job) and before any profile reload.
# Exit codes: 0 = valid, 1 = syntax error or missing required permission.
set -euo pipefail

PROFILE="${1:-deploy/apparmor.d/usr.local.bin.ventd}"

fail() {
    echo "FAIL: $*" >&2
    exit 1
}

# ── Existence ─────────────────────────────────────────────────────────────────
[[ -f "$PROFILE" ]] || fail "profile not found: $PROFILE"

# ── Syntax check ──────────────────────────────────────────────────────────────
# apparmor_parser -p parses without loading; fails on syntax errors.
if command -v apparmor_parser >/dev/null 2>&1; then
    if ! apparmor_parser -p "$PROFILE" >/dev/null 2>&1; then
        fail "apparmor_parser -p rejected $PROFILE"
    fi
    echo "OK  syntax check (apparmor_parser -p)"
else
    echo "SKIP syntax check (apparmor_parser not installed)"
fi

# ── Required permission checks ────────────────────────────────────────────────
# Each entry: "human label" "grep pattern"
declare -a CHECKS=(
    "etc/ventd/*.tmp rw"          '/etc/ventd/\*\.tmp.*rw'
    "sys/class/hwmon/** r"        '/sys/class/hwmon/\*\*.*r'
    "sys/devices/**/hwmon*/** r"  '/sys/devices/\*\*/hwmon\*.*r'
    "etc/ventd/** r"              '/etc/ventd/\*\*.*r'
    "proc/cpuinfo r"              '/proc/cpuinfo.*r'
    "proc/meminfo r"              '/proc/meminfo.*r'
    "dev/nvidia* rw"              '/dev/nvidia\*.*rw'
)

rc=0
i=0
while [[ $i -lt ${#CHECKS[@]} ]]; do
    label="${CHECKS[$i]}"
    pattern="${CHECKS[$((i+1))]}"
    if grep -qE "$pattern" "$PROFILE"; then
        echo "OK  $label"
    else
        echo "MISSING: $label (pattern: $pattern)" >&2
        rc=1
    fi
    i=$((i+2))
done

[[ $rc -eq 0 ]] || fail "one or more required permissions are absent from $PROFILE"
echo "PASS $PROFILE"
