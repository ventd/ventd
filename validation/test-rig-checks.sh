#!/usr/bin/env bash
# validation/test-rig-checks.sh — self-test for the two false-positive
# fixes in validation/run-rig-checks.sh (issue #61) AND for the 4h
# chip-binding assertion in validation/postreboot-verify.sh (issue #86).
#
# Sources both harnesses (each guarded by a BASH_SOURCE check so
# sourcing is side-effect-free) and invokes the check helpers against
# fixtures. All work happens in data; no root, no hwmon, no udev.

set -euo pipefail

cd "$(dirname "$0")"

# run-rig-checks.sh writes a log under OUTDIR; point it at a scratch
# dir so we don't create rig-check-*.log files in the worktree.
OUTDIR="$(mktemp -d)"
SCRATCH="$(mktemp -d)"
trap 'rm -rf "$OUTDIR" "$SCRATCH"' EXIT
export OUTDIR

# Source both harnesses. BASH_SOURCE guards keep the runners silent.
# shellcheck disable=SC1091
source ./run-rig-checks.sh
# shellcheck disable=SC1091
source ./postreboot-verify.sh

fail=0

expect_rc() {
    local want="$1" got="$2" label="$3"
    if [[ "$got" != "$want" ]]; then
        printf 'FAIL  %-64s  got rc=%s want %s\n' "$label" "$got" "$want"
        fail=1
    else
        printf 'PASS  %-64s  rc=%s\n' "$label" "$got"
    fi
}

# ── issue #61: run-rig-checks false positives ─────────────────────────────

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
neg="$SCRATCH/0aiii-neg.yaml"
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

# ── issue #86: postreboot-verify 4h chip-binding gate ─────────────────────

# build_fixture ROOT — builds a dual-nct6687 sysfs tree under ROOT that
# mirrors the exact layout phoenix-MS-7D25 exposed on the reboot where
# #86 was caught:
#
#   <ROOT>/devices/platform/nct6683.2592/           (hwmon5's target)
#   <ROOT>/devices/platform/nct6687.2592/           (hwmon6's target)
#   <ROOT>/sysfs/hwmon5/name                        "nct6687"
#   <ROOT>/sysfs/hwmon5/pwm1..pwm4                  empty files
#   <ROOT>/sysfs/hwmon5/device -> ../../devices/platform/nct6683.2592
#   <ROOT>/sysfs/hwmon6/name                        "nct6687"
#   <ROOT>/sysfs/hwmon6/pwm1..pwm4                  empty files
#   <ROOT>/sysfs/hwmon6/device -> ../../devices/platform/nct6687.2592
#
# Caller uses "$ROOT/sysfs" as the SYSFS_ROOT arg to assert_chip_binding.
build_fixture() {
    local root="$1"
    local dev_nct6683="$root/devices/platform/nct6683.2592"
    local dev_nct6687="$root/devices/platform/nct6687.2592"
    mkdir -p "$dev_nct6683" "$dev_nct6687"
    for slot in 5 6; do
        local h="$root/sysfs/hwmon${slot}"
        mkdir -p "$h"
        printf 'nct6687\n' >"$h/name"
        touch "$h/pwm1" "$h/pwm2" "$h/pwm3" "$h/pwm4"
    done
    ln -s "../../devices/platform/nct6683.2592" "$root/sysfs/hwmon5/device"
    ln -s "../../devices/platform/nct6687.2592" "$root/sysfs/hwmon6/device"
}

# write_cfg CFG_PATH PWM_HWMON  — writes a minimal dual-fan config that
# declares hwmon_device: .../nct6687.2592 but resolves pwm_path under
# /sysfs/hwmonN where N is the caller-supplied index. Match scenario uses
# 6 (aligned with hwmon_device); mismatch uses 5 (the other chip).
write_cfg() {
    local cfg="$1" hwmonN="$2" root="$3"
    cat >"$cfg" <<YAML
version: 1
poll_interval: 2s
web:
    listen: 0.0.0.0:9999
    password_hash: "\$2a\$12\$fixtureHashDoesNotAuthenticateAnythingReal0123456789"
sensors: []
fans:
    - name: Cpu Fan
      type: hwmon
      pwm_path: ${root}/sysfs/${hwmonN}/pwm1
      hwmon_device: ${root}/devices/platform/nct6687.2592
      chip_name: nct6687
      control_kind: pwm
      min_pwm: 12
      max_pwm: 255
curves: []
controls: []
YAML
}

match_root="$SCRATCH/chip-binding-match"
mismatch_root="$SCRATCH/chip-binding-mismatch"
build_fixture "$match_root"
build_fixture "$mismatch_root"

match_cfg="$SCRATCH/chip-binding-match.yaml"
mismatch_cfg="$SCRATCH/chip-binding-mismatch.yaml"
write_cfg "$match_cfg"    hwmon6 "$match_root"
write_cfg "$mismatch_cfg" hwmon5 "$mismatch_root"

# Match: pwm_path under hwmon6 whose device -> .../nct6687.2592; config
# hwmon_device also .../nct6687.2592 — every fan binds correctly. PASS.
rc=0
assert_chip_binding "$match_cfg" "$match_root/sysfs" >/dev/null 2>&1 || rc=$?
expect_rc 0 "$rc" "4h accepts fan whose hwmonN/device matches configured hwmon_device"

# Mismatch: pwm_path under hwmon5 whose device -> .../nct6683.2592 but
# config specifies .../nct6687.2592. This is the exact #86 boot-race
# symptom; the gate must FAIL it.
rc=0
assert_chip_binding "$mismatch_cfg" "$mismatch_root/sysfs" >/dev/null 2>&1 || rc=$?
expect_rc 1 "$rc" "4h rejects fan whose hwmonN/device does not match hwmon_device"

# Vacuous pass: config with no hwmon_device-carrying fans returns 77 so
# the postreboot runner renders it as SKIP rather than a spurious FAIL
# on rigs that haven't been disambiguated yet.
empty_cfg="$SCRATCH/chip-binding-vacuous.yaml"
cat >"$empty_cfg" <<'YAML'
version: 1
poll_interval: 2s
web:
    listen: 0.0.0.0:9999
    password_hash: "$2a$12$fixtureHashDoesNotAuthenticateAnythingReal0123456789"
sensors: []
fans: []
curves: []
controls: []
YAML
rc=0
assert_chip_binding "$empty_cfg" "$match_root/sysfs" >/dev/null 2>&1 || rc=$?
expect_rc 77 "$rc" "4h returns 77 (SKIP) when config has no hwmon_device-carrying fans"

if [[ "$fail" -ne 0 ]]; then
    printf '\ntest-rig-checks: FAIL (see above)\n' >&2
    exit 1
fi
printf '\ntest-rig-checks: PASS\n'
