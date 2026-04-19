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

# ── Enforce-mode check ────────────────────────────────────────────────────────
# Profile must NOT ship in complain mode. The flags= line must not contain
# the word "complain".
if grep -qE 'flags=.*complain' "$PROFILE"; then
    fail "profile ships in complain mode — must be enforce (remove 'complain' from flags=)"
fi
echo "OK  enforce-mode check (no complain flag)"

# ── Required permission checks ────────────────────────────────────────────────
# Each entry: "human label" "grep pattern"
declare -a CHECKS=(
    "etc/ventd/** rwk"              '/etc/ventd/\*\*.*rwk'
    "run/ventd/** rwk"              '/run/ventd/\*\*.*rwk'
    "var/lib/ventd/** rwk"          '/var/lib/ventd/\*\*.*rwk'
    "sys/class/hwmon/** r"          '/sys/class/hwmon/\*\*.*r'
    "sys/devices/**/hwmon/** r"     '/sys/devices/\*\*/hwmon/\*\*.*r'
    "sys/class/thermal"             '/sys/class/thermal'
    "sys/class/pwm"                 '/sys/class/pwm'
    "sys/devices/**/power_supply"   '/sys/devices/\*\*/power_supply'
    "dev/nvidia* rw"                '/dev/nvidia\*.*rw'
    "run/systemd/notify rw"         '/run/systemd/notify.*rw'
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

# ── Unit directive check ──────────────────────────────────────────────────────
# The service unit must declare AppArmorProfile=ventd so systemd forces
# the profile attachment by name, defeating the docker-default coexistence bug.
SERVICE="deploy/ventd.service"
if [[ -f "$SERVICE" ]]; then
    if grep -q 'AppArmorProfile=ventd' "$SERVICE"; then
        echo "OK  AppArmorProfile=ventd present in $SERVICE"
    else
        echo "MISSING: AppArmorProfile=ventd in $SERVICE" >&2
        rc=1
    fi
else
    echo "SKIP AppArmorProfile check ($SERVICE not found)"
fi

[[ $rc -eq 0 ]] || fail "one or more required permissions are absent from $PROFILE"
echo "PASS $PROFILE"
