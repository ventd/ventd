#!/usr/bin/env bash
# validation/test-rig-checks.sh — self-test for the two false-positive
# fixes in validation/run-rig-checks.sh (issue #61).
#
# Sources run-rig-checks.sh (which no-ops its runner when sourced, via
# a BASH_SOURCE guard) and invokes the two check functions against
# fixtures under validation/fixtures/. Asserts both return 0 (PASS).
#
# Intended to run under the CI shellcheck job. No root, no hwmon, no
# udev — pure data-in/data-out.

set -euo pipefail

cd "$(dirname "$0")"

# run-rig-checks.sh writes a log under OUTDIR; point it at a scratch
# dir so we don't create rig-check-*.log files in the worktree.
OUTDIR="$(mktemp -d)"
trap 'rm -rf "$OUTDIR"' EXIT
export OUTDIR

# Source the harness. The BASH_SOURCE guard keeps the runner silent.
# shellcheck disable=SC1091
source ./run-rig-checks.sh

fail=0

expect_rc() {
    local want="$1" got="$2" label="$3"
    if [[ "$got" != "$want" ]]; then
        printf 'FAIL  %-60s  got rc=%s want %s\n' "$label" "$got" "$want"
        fail=1
    else
        printf 'PASS  %-60s  rc=%s\n' "$label" "$got"
    fi
}

# 0a.i — udev rule with a COMMENTED ATTR{name} example must not trip
# the chip-agnostic predicate.
rc=0
VENTD_UDEV_RULE=./fixtures/udev-commented-attr.rules \
    check_0a_i_udev_rule_chip_agnostic >/dev/null 2>&1 || rc=$?
expect_rc 0 "$rc" "0a.i accepts commented ATTR{name} example (fixture)"

# 0a.iii — config containing a non-hwmon (nvidia) fan alongside
# correctly-enriched hwmon fans must not be treated as missing
# chip_name on the nvidia one.
rc=0
VENTD_CONFIG_YAML=./fixtures/config-with-nvidia-fan.yaml \
    check_0a_iii_enrich_chip_name_in_config >/dev/null 2>&1 || rc=$?
expect_rc 0 "$rc" "0a.iii accepts hwmon+nvidia mix, chip_name on hwmon only"

# Negative control: an hwmon fan with no chip_name must still FAIL.
neg="$(mktemp --suffix=.yaml)"
trap 'rm -rf "$OUTDIR" "$neg"' EXIT
cat >"$neg" <<'YAML'
version: 1
poll_interval: 2s
web:
    listen: 0.0.0.0:9999
    password_hash: "$2a$12$fixtureHashDoesNotAuthenticateAnythingReal0123456789"
sensors: []
fans:
    - name: missing
      type: hwmon
      pwm_path: /sys/class/hwmon/hwmon99/pwm1
      min_pwm: 12
      max_pwm: 255
curves: []
controls: []
YAML
rc=0
VENTD_CONFIG_YAML="$neg" \
    check_0a_iii_enrich_chip_name_in_config >/dev/null 2>&1 || rc=$?
expect_rc 1 "$rc" "0a.iii still FAILs when hwmon fan truly has no chip_name"

if [[ "$fail" -ne 0 ]]; then
    printf '\ntest-rig-checks: FAIL (see above)\n' >&2
    exit 1
fi
printf '\ntest-rig-checks: PASS\n'
