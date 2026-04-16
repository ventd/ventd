#!/usr/bin/env bash
# check-unit-ordering.sh — regression guard for ventd.service ordering.
#
# The v0.3 hwmon topology work (#95 / #98) replaced the original
# After=systemd-udev-settle.service ordering with a positive readiness
# check: ExecStartPre=-/usr/local/sbin/ventd-wait-hwmon (PR #105)
# plus the in-binary retry inside config.LoadForStartup. udev-settle is
# deprecated on current distros and was a no-op on hosts whose kernel
# ships the hwmon drivers built in, so relying on it was always a best-
# effort hint rather than a guarantee.
#
# This script fails if either half of the new invariant slips:
#   1. [Unit].After= MUST NOT list systemd-udev-settle.service — the
#      deprecated ordering must not come back silently via a merge or a
#      templating error.
#   2. [Service] MUST declare ExecStartPre=-/usr/local/sbin/ventd-wait-hwmon
#      so the cold-boot race the settle ordering used to paper over is
#      still addressed by the positive readiness gate.
#
# Paired with scripts/check-unit-onfailure.sh — same pattern, different
# directive — so both invariants are asserted at PR time rather than
# next reboot. Intended to be run locally and from the CI shellcheck
# job.

set -euo pipefail

UNIT="${1:-deploy/ventd.service}"
FORBIDDEN_AFTER="systemd-udev-settle.service"
REQUIRED_EXECSTARTPRE="/usr/local/sbin/ventd-wait-hwmon"

if [[ ! -f "$UNIT" ]]; then
    printf 'check-unit-ordering: %s not found\n' "$UNIT" >&2
    exit 2
fi

# Extract every non-comment After= value under [Unit] and every
# ExecStartPre= command under [Service]. Single awk pass; section-
# tracking idiom kept symmetric with scripts/check-unit-onfailure.sh.
# Values are space-separated within each field; the two fields are
# tab-separated on a single line so `IFS=$'\t' read` below preserves
# internal spacing even when After= lists multiple units.
IFS=$'\t' read -r after_values execstartpre_values < <(awk '
BEGIN { section = ""; after = ""; pre = "" }
/^[[:space:]]*#/ { next }
/^\[.*\][[:space:]]*$/ {
    section = $0
    sub(/^[[:space:]]*\[/, "", section)
    sub(/\][[:space:]]*$/, "", section)
    next
}
section == "Unit" && /^[[:space:]]*After=/ {
    v = $0
    sub(/^[[:space:]]*After=/, "", v)
    gsub(/[[:space:]]+$/, "", v)
    after = (after == "" ? v : after " " v)
}
section == "Service" && /^[[:space:]]*ExecStartPre=/ {
    v = $0
    sub(/^[[:space:]]*ExecStartPre=/, "", v)
    sub(/^-/, "", v)   # strip the leading "-" that marks non-fatal
    gsub(/[[:space:]]+$/, "", v)
    # ExecStartPre can carry arguments; the canonical form we guard is
    # the command path, so key off the first whitespace-delimited token.
    split(v, parts, /[[:space:]]+/)
    pre = (pre == "" ? parts[1] : pre " " parts[1])
}
END { printf "%s\t%s\n", after, pre }
' "$UNIT")

fail=0

if printf '%s\n' "$after_values" | tr ' ' '\n' | grep -Fxq "$FORBIDDEN_AFTER"; then
    printf 'FAIL: [Unit].After= still lists %s in %s\n' "$FORBIDDEN_AFTER" "$UNIT" >&2
    printf '      That ordering was removed in v0.3 because udev-settle is\n' >&2
    printf '      deprecated. Use ExecStartPre=ventd-wait-hwmon instead (the\n' >&2
    printf '      positive readiness gate introduced in PR #105).\n' >&2
    fail=1
fi

if ! printf '%s\n' "$execstartpre_values" | tr ' ' '\n' | grep -Fxq "$REQUIRED_EXECSTARTPRE"; then
    printf 'FAIL: [Service].ExecStartPre= does not include %s in %s\n' \
        "$REQUIRED_EXECSTARTPRE" "$UNIT" >&2
    printf '      current ExecStartPre= commands under [Service]:\n' >&2
    if [[ -n "$execstartpre_values" ]]; then
        printf '%s\n' "$execstartpre_values" | tr ' ' '\n' | sed 's/^/        /' >&2
    else
        printf '        (none)\n' >&2
    fi
    printf '      Without this gate, ventd can start resolving hwmon paths\n' >&2
    printf '      before every configured chip has been enumerated by udev\n' >&2
    printf '      and silently mis-bind on multi-chip boards (#86, #103).\n' >&2
    fail=1
fi

if [[ $fail -ne 0 ]]; then
    exit 1
fi

printf 'PASS: %s drops After=%s and declares ExecStartPre=%s\n' \
    "$UNIT" "$FORBIDDEN_AFTER" "$REQUIRED_EXECSTARTPRE"
