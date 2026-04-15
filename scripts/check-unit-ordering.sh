#!/usr/bin/env bash
# check-unit-ordering.sh — regression guard for ventd.service ordering.
#
# On dual-Super-I/O boards (e.g. MSI Z690/Z790 with both nct6683d and
# nct6687d drivers loaded) udev can finish enumerating the second hwmon
# chip up to ~6 s after userspace boot. Without After=systemd-udev-settle
# the ventd daemon starts resolving chip_name + hwmon_device before all
# chips are in place; #86 was the silent mis-bind that resulted.
#
# This script fails if the shipped deploy/ventd.service does NOT declare
# After=systemd-udev-settle.service in its [Unit] section. Paired with
# scripts/check-unit-onfailure.sh — same pattern, different directive —
# so both invariants are asserted at PR time rather than next reboot.
#
# Intended to be run locally and from the CI shellcheck job.

set -euo pipefail

UNIT="${1:-deploy/ventd.service}"
REQUIRED_AFTER="systemd-udev-settle.service"

if [[ ! -f "$UNIT" ]]; then
    printf 'check-unit-ordering: %s not found\n' "$UNIT" >&2
    exit 2
fi

# Pull every non-comment After= value under the [Unit] section. Uses the
# same section-tracking idiom as scripts/check-unit-onfailure.sh so the
# two guards stay symmetric.
after_values="$(awk -v section_wanted=Unit '
BEGIN { section = "" }
/^[[:space:]]*#/         { next }
/^\[.*\][[:space:]]*$/ {
    section = $0
    sub(/^[[:space:]]*\[/, "", section)
    sub(/\][[:space:]]*$/, "", section)
    next
}
section == section_wanted && /^[[:space:]]*After=/ {
    v = $0
    sub(/^[[:space:]]*After=/, "", v)
    gsub(/[[:space:]]+$/, "", v)
    print v
}
' "$UNIT")"

if printf '%s\n' "$after_values" | tr ' ' '\n' | grep -Fxq "$REQUIRED_AFTER"; then
    printf 'PASS: [Unit].After= includes %s in %s\n' "$REQUIRED_AFTER" "$UNIT"
    exit 0
fi

printf 'FAIL: [Unit].After= does not include %s in %s\n' "$REQUIRED_AFTER" "$UNIT" >&2
printf '      current After= values under [Unit]:\n' >&2
if [[ -n "$after_values" ]]; then
    printf '%s\n' "$after_values" | sed 's/^/        /' >&2
else
    printf '        (none)\n' >&2
fi
printf '      This is the regression guard for #86: without this After=,\n' >&2
printf '      ventd can start resolving hwmon paths before udev finishes\n' >&2
printf '      enumerating multi-chip boards and silently mis-bind (#86).\n' >&2
exit 1
