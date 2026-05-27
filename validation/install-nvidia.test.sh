#!/usr/bin/env bash
# Fixture test for issue #461 — scripts/install.sh must detect the NVIDIA
# control device's owning group and add the ventd user to it after account
# creation, without hard-coding "video".
#
# This test exercises _ventd_add_nvidia_group in isolation. It sources just
# that helper from scripts/install.sh (via sed, same pattern as
# install-pwm-holders.test.sh) and drives it against a mock device node via
# the _VENTD_NVIDIACTL_PATH test hook. A stub usermod in a temp bin dir
# records the exact arguments that would be passed on a real system.
#
# Does NOT require root, a running NVIDIA driver, or an actual ventd user.
#
# Exit 0 on pass; non-zero and a FAIL line on any assertion miss.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
INSTALL_SH="$REPO_ROOT/scripts/install.sh"

SCRATCH="$(mktemp -d -t ventd-install-nvidia-XXXX)"
trap 'rm -rf "$SCRATCH"' EXIT

# Extract the _ventd_add_nvidia_group function from install.sh.
# The sed range grabs the function body from its declaration to the closing
# column-0 "}"; same technique as validation/install-pwm-holders.test.sh.
HELPER_SRC="$SCRATCH/_ventd_add_nvidia_group.sh"
sed -n '/^_ventd_add_nvidia_group()/,/^}/p' "$INSTALL_SH" > "$HELPER_SRC"
if [[ ! -s "$HELPER_SRC" ]]; then
    echo "FAIL: could not extract _ventd_add_nvidia_group from $INSTALL_SH" >&2
    exit 1
fi
# shellcheck source=/dev/null
source "$HELPER_SRC"

# Stub bin dir: put fake usermod and id ahead of the real ones in PATH.
STUB_BIN="$SCRATCH/bin"
mkdir -p "$STUB_BIN"
USERMOD_LOG="$SCRATCH/usermod.log"

# Stub usermod: logs its args so tests can assert the right group was passed.
cat > "$STUB_BIN/usermod" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${USERMOD_LOG}"
EOF
chmod +x "$STUB_BIN/usermod"
export USERMOD_LOG

# Stub id: returns a synthetic line that does NOT include the test group so
# _ventd_add_nvidia_group always reaches the usermod branch in "absent" cases.
# Each test case can override id via a fresh stub.
cat > "$STUB_BIN/id" <<'EOF'
#!/usr/bin/env bash
echo "uid=999(ventd) gid=999(ventd) groups=999(ventd)"
EOF
chmod +x "$STUB_BIN/id"

export PATH="$STUB_BIN:$PATH"

# Create a mock /dev/nvidiactl using a regular file. We cannot chown it to
# root:video without root, so we rely on stat returning the current process's
# group (whichever it is). The test asserts that the extracted group name
# (not literally "video") is passed to usermod — this proves the group is
# derived from the device node, not hard-coded.
MOCK_CTL="$SCRATCH/nvidiactl"
touch "$MOCK_CTL"
chmod 0660 "$MOCK_CTL"
# Discover what group stat will report for this file.
MOCK_GROUP="$(stat -c '%G' "$MOCK_CTL" 2>/dev/null)"
export _VENTD_NVIDIACTL_PATH="$MOCK_CTL"

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

echo "== _ventd_add_nvidia_group"

# ── Test 1: device absent — usermod must not be called ──────────────────────
: > "$USERMOD_LOG"
export _VENTD_NVIDIACTL_PATH="$SCRATCH/nonexistent-device"
_ventd_add_nvidia_group 2>/dev/null
absent_calls="$(wc -l < "$USERMOD_LOG" | tr -d ' ')"
check "absent device: usermod not called" "$([ "$absent_calls" -eq 0 ] && echo 1 || echo 0)"

# Tests 2 + 3 need a mock device whose owning group is NOT "root" (so the
# helper proceeds past its root-owned-device early-return). A root-created
# file is root:root, so these only run meaningfully as a non-root user —
# skip them otherwise (probe-and-skip; the suite header notes "does NOT
# require root", i.e. is designed for the non-root case).
if [[ "$MOCK_GROUP" != "root" ]]; then
    # ── Test 2: device present, ventd not in group — usermod called with group ──
    : > "$USERMOD_LOG"
    export _VENTD_NVIDIACTL_PATH="$MOCK_CTL"
    # Stub id to NOT include the mock group.
    cat > "$STUB_BIN/id" <<EOF
#!/usr/bin/env bash
echo "uid=999(ventd) gid=999(ventd) groups=999(ventd)"
EOF
    chmod +x "$STUB_BIN/id"
    _ventd_add_nvidia_group 2>/dev/null
    present_calls="$(wc -l < "$USERMOD_LOG" | tr -d ' ')"
    check "device present, not in group: usermod called" "$([ "$present_calls" -eq 1 ] && echo 1 || echo 0)"
    # The usermod invocation must include -aG and the group discovered from stat.
    if [[ "$present_calls" -ge 1 ]]; then
        usermod_args="$(cat "$USERMOD_LOG")"
        check "usermod args contain -aG" "$(echo "$usermod_args" | grep -q '\-aG' && echo 1 || echo 0)"
        check "usermod args contain device group" "$(echo "$usermod_args" | grep -q "$MOCK_GROUP" && echo 1 || echo 0)"
    fi

    # ── Test 3: device present, ventd already in group — usermod NOT called ─────
    : > "$USERMOD_LOG"
    export _VENTD_NVIDIACTL_PATH="$MOCK_CTL"
    # Stub id to INCLUDE the mock group. Write to a tmp then rename atomically so
    # "Text file busy" doesn't occur if a prior execution still holds the inode.
    cat > "$STUB_BIN/id.tmp" <<EOF
#!/usr/bin/env bash
echo "uid=999(ventd) gid=999(ventd) groups=999(ventd),$(id -g)(${MOCK_GROUP})"
EOF
    chmod +x "$STUB_BIN/id.tmp"
    mv -f "$STUB_BIN/id.tmp" "$STUB_BIN/id"
    _ventd_add_nvidia_group 2>/dev/null
    already_calls="$(wc -l < "$USERMOD_LOG" | tr -d ' ')"
    check "already in group: usermod not called" "$([ "$already_calls" -eq 0 ] && echo 1 || echo 0)"
else
    printf '  [SKIP] device-present group tests (running as root: mock device is root-owned)\n'
fi

# ── Test 4: device owned by root — skip (no group fix needed) ───────────────
: > "$USERMOD_LOG"
ROOT_CTL="$SCRATCH/nvidiactl-root"
touch "$ROOT_CTL"
chmod 0600 "$ROOT_CTL"
# stat -c '%G' on a file owned by root returns "root"
# We simulate that by temporarily stubbing stat.
cat > "$STUB_BIN/stat" <<'EOF'
#!/usr/bin/env bash
echo "root"
EOF
chmod +x "$STUB_BIN/stat"
export _VENTD_NVIDIACTL_PATH="$ROOT_CTL"
_ventd_add_nvidia_group 2>/dev/null
root_calls="$(wc -l < "$USERMOD_LOG" | tr -d ' ')"
check "root-owned device: usermod not called" "$([ "$root_calls" -eq 0 ] && echo 1 || echo 0)"
# Remove stat stub so subsequent tests use the real stat.
rm "$STUB_BIN/stat"

# ── _ventd_verify_nvml: probe as the ACTUAL service user, no false warning ──
# Follow-up to #461: the post-install NVML check must reflect how the daemon
# reaches NVML (User= from the unit — root on the shipped unit), not a
# hard-coded `sudo -u ventd`, so a root daemon (or world-readable /dev/nvidia*)
# never triggers a spurious "GPU features may be disabled" + usermod hint.
echo ""
echo "== _ventd_verify_nvml"

# Extract both verify helpers (declaration → column-0 "}").
VERIFY_SRC="$SCRATCH/_ventd_verify_nvml.sh"
sed -n '/^_ventd_service_user()/,/^}/p;/^_ventd_verify_nvml()/,/^}/p' "$INSTALL_SH" > "$VERIFY_SRC"
if [[ ! -s "$VERIFY_SRC" ]]; then
    echo "FAIL: could not extract verify helpers from $INSTALL_SH" >&2
    exit 1
fi
# shellcheck source=/dev/null
source "$VERIFY_SRC"

ROOT_UNIT="$SCRATCH/ventd-root.service";     printf '[Service]\nUser=root\n'      > "$ROOT_UNIT"
VENTD_UNIT="$SCRATCH/ventd-ventd.service";   printf '[Service]\nUser=ventd\n'     > "$VENTD_UNIT"
NOUSER_UNIT="$SCRATCH/ventd-nouser.service"; printf '[Service]\nExecStart=/x\n'   > "$NOUSER_UNIT"

# Service-user resolution from the unit.
check "service_user reads User=root"  "$([ "$(_VENTD_UNIT_PATH=$ROOT_UNIT _ventd_service_user)"  = "root"  ] && echo 1 || echo 0)"
check "service_user reads User=ventd" "$([ "$(_VENTD_UNIT_PATH=$VENTD_UNIT _ventd_service_user)" = "ventd" ] && echo 1 || echo 0)"
check "service_user defaults to root when User= absent" "$([ "$(_VENTD_UNIT_PATH=$NOUSER_UNIT _ventd_service_user)" = "root" ] && echo 1 || echo 0)"

# Passing nvidia-smi → "accessible" line naming the service user.
cat > "$STUB_BIN/nvidia-smi" <<'EOF'
#!/usr/bin/env bash
echo "NVIDIA-SMI 595.71.05"
EOF
chmod +x "$STUB_BIN/nvidia-smi"
out_ok="$(_VENTD_UNIT_PATH=$ROOT_UNIT _ventd_verify_nvml 2>&1)"
check "root + NVML ok → accessible (no warning)" "$(echo "$out_ok" | grep -q 'NVML accessible from the ventd service (user: root)' && echo 1 || echo 0)"

# Failing nvidia-smi: root → warn but NO usermod hint; ventd → warn WITH hint.
cat > "$STUB_BIN/nvidia-smi" <<'EOF'
#!/usr/bin/env bash
echo "Failed to initialize NVML: Insufficient Permissions" >&2; exit 1
EOF
chmod +x "$STUB_BIN/nvidia-smi"
out_root_fail="$(_VENTD_UNIT_PATH=$ROOT_UNIT _ventd_verify_nvml 2>&1)"
check "root + NVML fail → warns, NO usermod hint" "$(echo "$out_root_fail" | grep -q 'verification failed' && ! echo "$out_root_fail" | grep -q 'usermod' && echo 1 || echo 0)"
out_ventd_fail="$(_VENTD_UNIT_PATH=$VENTD_UNIT _ventd_verify_nvml 2>&1)"
check "ventd + NVML fail → warns WITH usermod hint" "$(echo "$out_ventd_fail" | grep -q 'usermod -aG video ventd' && echo 1 || echo 0)"

# No nvidia-smi on PATH → silent no-op.
rm -f "$STUB_BIN/nvidia-smi"
out_none="$(PATH="$STUB_BIN" _VENTD_UNIT_PATH=$ROOT_UNIT _ventd_verify_nvml 2>&1)"
check "no nvidia-smi → silent" "$([ -z "$out_none" ] && echo 1 || echo 0)"

echo ""
echo "Results: $pass passed, $fail failed"
[[ "$fail" -eq 0 ]]
