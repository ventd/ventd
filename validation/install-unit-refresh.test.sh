#!/usr/bin/env bash
# Fixture test for issue #60 — install.sh must refresh systemd unit files
# on every run, detect when the on-disk unit changed, and print a single
# "unit files updated" line only when it actually swapped the file.
#
# Runs scripts/install.sh in VENTD_TEST_MODE against a scratch sysroot so
# no side effects touch the host (no systemctl, no udevadm, no account
# creation, no hwmon probe, no mac loaders).
#
# Exit 0 on pass; non-zero (and a FAIL line) on any assertion miss.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

SCRATCH="$(mktemp -d -t ventd-install-refresh-XXXX)"
trap 'rm -rf "$SCRATCH"' EXIT

SYSTEMD_DIR="$SCRATCH/etc/systemd/system"
ETC_DIR="$SCRATCH/etc/ventd"
PREFIX="$SCRATCH/usr/local/bin"
mkdir -p "$SYSTEMD_DIR" "$ETC_DIR" "$PREFIX"

SERVICE_FILE="$SYSTEMD_DIR/ventd.service"
RECOVER_FILE="$SYSTEMD_DIR/ventd-recover.service"

SERVICE_SRC="$REPO_ROOT/deploy/ventd.service"
RECOVER_SRC="$REPO_ROOT/deploy/ventd-recover.service"

# Tiny stub binary. install.sh just copies it.
STUB_BINARY="$SCRATCH/ventd-stub"
: > "$STUB_BINARY"
chmod 0755 "$STUB_BINARY"

fail=0
pass=0

note() { printf '  %s\n' "$*"; }
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

run_install() {
    local log="$1"
    env VENTD_TEST_MODE=1 \
        VENTD_PREFIX="$PREFIX" \
        VENTD_SYSTEMD_DIR="$SYSTEMD_DIR" \
        VENTD_ETC_DIR="$ETC_DIR" \
        bash "$REPO_ROOT/scripts/install.sh" "$STUB_BINARY" \
        >"$log" 2>&1
}

# ── Run 1: fresh install, units should land and "updated" must be logged ────
echo "== Run 1 — fresh scratch sysroot"
LOG1="$SCRATCH/run1.log"
if run_install "$LOG1"; then
    note "install.sh returned 0"
else
    note "install.sh exit code: $?"
    cat "$LOG1"
    exit 1
fi

check "ventd.service landed"                   "$([[ -f $SERVICE_FILE ]]   && echo 1 || echo 0)"
check "ventd-recover.service landed"           "$([[ -f $RECOVER_FILE ]]   && echo 1 || echo 0)"
check "ventd.service matches deploy source"    "$(cmp -s "$SERVICE_SRC" "$SERVICE_FILE" && echo 1 || echo 0)"
check "ventd-recover.service matches source"   "$(cmp -s "$RECOVER_SRC" "$RECOVER_FILE" && echo 1 || echo 0)"
check "ventd.service is mode 0644"             "$([[ $(stat -c %a "$SERVICE_FILE") == "644" ]] && echo 1 || echo 0)"
check "ventd-recover.service is mode 0644"     "$([[ $(stat -c %a "$RECOVER_FILE") == "644" ]] && echo 1 || echo 0)"
check "binary copied to VENTD_PREFIX"          "$([[ -x "$PREFIX/ventd" ]] && echo 1 || echo 0)"
check "'unit files updated' logged on run 1"   "$(grep -Fq 'unit files updated' "$LOG1" && echo 1 || echo 0)"

# ── Run 2: re-run unchanged — "updated" must NOT appear ────────────────────
echo "== Run 2 — rerun with no repo changes"
LOG2="$SCRATCH/run2.log"
run_install "$LOG2"

check "ventd.service still matches source"     "$(cmp -s "$SERVICE_SRC" "$SERVICE_FILE" && echo 1 || echo 0)"
check "'unit files updated' NOT logged on rerun" "$(grep -Fq 'unit files updated' "$LOG2" && echo 0 || echo 1)"

# ── Run 3: corrupt the on-disk unit, re-run — must refresh + log 'updated' ─
echo "== Run 3 — rerun after corrupting installed unit file"
printf 'corrupted by test\n' > "$SERVICE_FILE"
LOG3="$SCRATCH/run3.log"
run_install "$LOG3"

check "ventd.service restored to deploy source" "$(cmp -s "$SERVICE_SRC" "$SERVICE_FILE" && echo 1 || echo 0)"
check "'unit files updated' logged after corruption fix" "$(grep -Fq 'unit files updated' "$LOG3" && echo 1 || echo 0)"

# ── Run 4: corrupt only the recover unit, re-run — same expectation ────────
echo "== Run 4 — rerun after corrupting ventd-recover.service"
printf 'corrupted recover unit\n' > "$RECOVER_FILE"
LOG4="$SCRATCH/run4.log"
run_install "$LOG4"

check "ventd-recover.service restored"          "$(cmp -s "$RECOVER_SRC" "$RECOVER_FILE" && echo 1 || echo 0)"
check "'unit files updated' logged (recover change)" "$(grep -Fq 'unit files updated' "$LOG4" && echo 1 || echo 0)"

echo
echo "=========================================="
echo "  pass: $pass"
echo "  fail: $fail"
echo "=========================================="

if (( fail > 0 )); then
    echo "Last run log:"
    cat "$LOG4"
    exit 1
fi
exit 0
