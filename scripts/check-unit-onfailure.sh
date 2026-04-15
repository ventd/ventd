#!/usr/bin/env bash
# check-unit-onfailure.sh — regression guard for ventd.service.
#
# systemd accepts OnFailure= only in [Unit]. When placed under [Service]
# it is silently ignored, and ventd-recover.service never fires on
# SIGKILL / OOM / panic-escape, quietly breaking the README's "any exit
# path within two seconds" safety promise.
#
# This script fails if deploy/ventd.service contains an OnFailure=
# directive anywhere except the [Unit] section. Intended to be run
# locally and from CI.

set -euo pipefail

UNIT="${1:-deploy/ventd.service}"

if [[ ! -f "$UNIT" ]]; then
    printf 'check-unit-onfailure: %s not found\n' "$UNIT" >&2
    exit 2
fi

awk '
BEGIN { section = ""; bad = 0 }
/^[[:space:]]*#/ { next }
/^\[.*\][[:space:]]*$/ {
    section = $0
    sub(/^[[:space:]]*\[/, "", section)
    sub(/\][[:space:]]*$/, "", section)
    next
}
/^[[:space:]]*OnFailure=/ {
    if (section != "Unit") {
        printf "FAIL: OnFailure= found in [%s] at line %d: %s\n", section, NR, $0
        bad = 1
    }
}
END { exit bad }
' "$UNIT"

printf 'PASS: OnFailure= is in [Unit] (or absent) in %s\n' "$UNIT"
