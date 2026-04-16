#!/usr/bin/env bash
# Fixture test for issue #107 — scripts/install.sh's check_pwm_holders
# preflight must carve out the upgrade case: if ventd is already running and
# every PID holding /sys/class/hwmon/*/pwm<N> open is itself a ventd process,
# the preflight passes (the installer's try-restart will swap the binary).
# Any non-ventd holder (a competing fan-control daemon) still fails loud.
#
# This test exercises _pwm_holders_all_ventd in isolation. It sources just
# that helper from scripts/install.sh (extracted via sed; install.sh otherwise
# runs top-to-bottom with side effects) and drives it against a fake procfs
# tree rooted in a mktemp dir via the _VENTD_PROC_DIR test hook.
#
# Exit 0 on pass; non-zero and a FAIL line on any assertion miss.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
INSTALL_SH="$REPO_ROOT/scripts/install.sh"

SCRATCH="$(mktemp -d -t ventd-pwm-holders-XXXX)"
trap 'rm -rf "$SCRATCH"' EXIT

# Extract only the helper we need from install.sh. The surrounding script
# runs side-effectful code at load time (root check, service installs), so
# sourcing the whole file is a non-starter. The sed range grabs the first
# body of _pwm_holders_all_ventd() up to its closing column-0 "}".
HELPER_SRC="$SCRATCH/_pwm_holders_all_ventd.sh"
sed -n '/^_pwm_holders_all_ventd()/,/^}/p' "$INSTALL_SH" > "$HELPER_SRC"
if [[ ! -s "$HELPER_SRC" ]]; then
    echo "FAIL: could not extract _pwm_holders_all_ventd from $INSTALL_SH" >&2
    exit 1
fi
# shellcheck source=/dev/null
source "$HELPER_SRC"

# Seed a fake procfs under $SCRATCH/proc with synthetic PIDs. Each entry
# is a directory containing a "comm" file. _VENTD_PROC_DIR points the
# helper at this tree.
PROC="$SCRATCH/proc"
mkdir -p \
    "$PROC/1001" \
    "$PROC/1002" \
    "$PROC/2001"
printf 'ventd\n'        > "$PROC/1001/comm"
printf 'ventd\n'        > "$PROC/1002/comm"
printf 'fancontrol\n'   > "$PROC/2001/comm"
export _VENTD_PROC_DIR="$PROC"

fail=0
pass=0
check() {
    local msg="$1" ok="$2"
    if [[ "$ok" == "1" ]]; then
        printf '  [PASS] %s\n' "$msg"
        pass=$(( pass + 1 ))
    else
        printf '  [FAIL] %s\n' "$msg"
        fail=$(( fail + 1 ))
    fi
}

run_ok()   { _pwm_holders_all_ventd "$1" >/dev/null 2>&1; }
run_fail() { ! _pwm_holders_all_ventd "$1" >/dev/null 2>&1; }

echo "== _pwm_holders_all_ventd"

# Single ventd PID → carve-out allows install to proceed.
check "single ventd PID accepted"           "$(run_ok   '1001'         && echo 1 || echo 0)"

# Multiple ventd PIDs (parent + child after try-restart overlap) → accepted.
check "multiple ventd PIDs accepted"        "$(run_ok   '1001 1002'    && echo 1 || echo 0)"

# A non-ventd competitor in the list → must fail loud (fancontrol running).
check "non-ventd competitor rejected"       "$(run_fail '1001 2001'    && echo 1 || echo 0)"

# Only a non-ventd PID → reject.
check "only non-ventd PID rejected"         "$(run_fail '2001'         && echo 1 || echo 0)"

# Unknown PID (/proc/<pid>/comm missing) → conservative reject; we'd rather
# surface the generic error than silently pass a PID we can't identify.
check "unknown PID rejected"                "$(run_fail '9999'         && echo 1 || echo 0)"

# Empty holder list → reject (the caller skips the check before invoking us
# when fuser returned nothing; we enforce any==1 as a defensive posture).
check "empty holders rejected"              "$(run_fail ''             && echo 1 || echo 0)"

# fuser output occasionally tags PIDs with a mode suffix (e.g. "1001c" for
# "f_owner"). Strip non-digits per-field and still accept pure-ventd lists.
check "fuser mode-suffixed PIDs accepted"   "$(run_ok   '1001c 1002e'  && echo 1 || echo 0)"

# A mixed list where the suffixed PID belongs to fancontrol still fails.
check "mode-suffixed competitor rejected"   "$(run_fail '1001c 2001e'  && echo 1 || echo 0)"

echo
echo "pass=$pass  fail=$fail"

if (( fail > 0 )); then
    echo "install-pwm-holders.test.sh: FAIL"
    exit 1
fi
echo "install-pwm-holders.test.sh: PASS"
