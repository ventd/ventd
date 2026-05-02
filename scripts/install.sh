#!/usr/bin/env bash
# CRLF self-heal — if this script was copied through a Windows path
# (USB stick, SMB share, WinSCP) the shell chokes on carriage returns
# and fails with a "bad interpreter" error or a syntax error at the
# first function definition. Detect + sed + re-exec in a single `&&`
# chain: bash can't tokenize multi-line `if/fi` keywords when every
# line is terminated with \r\n (the `fi` becomes `fi\r` and is never
# recognized as the fi keyword). Single-line chaining sidesteps that.
# Placed immediately after the shebang — before any blank line that
# would become a `$'\r': command not found` error on the first pass.
# The [[ -f "$0" ]] guard makes this a no-op under curl-pipe-bash
# (where $0 is "bash", not a file). See GitHub #196.
[[ -f "$0" ]] && grep -lq $'\r' "$0" 2>/dev/null && sed -i 's/\r$//' "$0" && exec bash "$0" "$@"
# ventd install script
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh | sudo bash
#   sudo ./install.sh                         # from an extracted release tarball
#   sudo ./install.sh /path/to/ventd-binary   # install a locally-built binary
#
# Environment overrides:
#   VENTD_VERSION                     Pin to a specific release tag (e.g. v0.4.0). Default: latest.
#   VENTD_REPO                        GitHub repo slug. Default: ventd/ventd.
#   VENTD_PREFIX                      Install prefix for the binary. Default: /usr/local/bin.
#   VENTD_INSTALL_POSTREBOOT_VERIFY   Set to 1 to install + enable the
#                                     ventd-postreboot-verify oneshot
#                                     unit. Fires once on next boot
#                                     and logs PASS/FAIL under
#                                     /var/log/ventd/. Default: off.
#
# What this script does:
#   1. If no local binary is provided, downloads the release tarball for the
#      host architecture, verifies its SHA-256 against checksums.txt, and
#      extracts it to a temporary directory.
#   2. Copies the binary to $VENTD_PREFIX/ventd.
#   3. Creates /etc/ventd/ for config and calibration data.
#   4. Installs the service unit for the detected init system
#      (systemd, OpenRC, or runit).
#   5. Enables AND starts the service. The daemon prints a one-time setup
#      token to its log on first boot — the web UI's first-boot wizard
#      prompts for it, and you can recover it with journalctl.
#
# After installation, open http://<this-machine-ip>:9999 in your browser.

set -euo pipefail

VENTD_REPO="${VENTD_REPO:-ventd/ventd}"
VENTD_PREFIX="${VENTD_PREFIX:-/usr/local/bin}"
VENTD_VERSION="${VENTD_VERSION:-}"

# Path overrides (primarily for the install-unit-refresh fixture test under
# validation/ and for the installer-registers-recover-unit regression test
# at cmd/ventd-recover/installer_regression_test.go; defaults match the
# shipped layout).
VENTD_SYSTEMD_DIR="${VENTD_SYSTEMD_DIR:-/etc/systemd/system}"
VENTD_ETC_DIR="${VENTD_ETC_DIR:-/etc/ventd}"
VENTD_SBIN_DIR="${VENTD_SBIN_DIR:-/usr/local/sbin}"
VENTD_STATE_DIR="${VENTD_STATE_DIR:-/var/lib/ventd}"

# Test-mode short-circuits. When set to "1", destructive operations
# against the running system (systemctl, udevadm, account creation,
# hwmon module probe, apparmor/selinux loaders, pwm conflict checks,
# port probes, root gate) are skipped — but every file-copy path still
# executes so the unit-file refresh behavior can be exercised against
# a scratch sysroot. See validation/install-unit-refresh.test.sh.
VENTD_TEST_MODE="${VENTD_TEST_MODE:-0}"

TMPDIR_CLEANUP=""
cleanup() {
    if [[ -n "$TMPDIR_CLEANUP" && -d "$TMPDIR_CLEANUP" ]]; then
        rm -rf "$TMPDIR_CLEANUP"
    fi
}
trap cleanup EXIT

# ── Root check ───────────────────────────────────────────────────────────────

if [[ $EUID -ne 0 && "$VENTD_TEST_MODE" != "1" ]]; then
    echo "error: this script must be run as root (use sudo)" >&2
    exit 1
fi

# ── Resolve source: local binary, local tarball layout, or remote release ───

# When the script is read from a pipe (curl | bash), BASH_SOURCE may be empty
# or point to /dev/stdin. Detect that and skip $SCRIPT_DIR resolution.
if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    SCRIPT_DIR=""
fi

BINARY=""
ASSET_DIR=""   # where to find service unit files; set below

if [[ $# -ge 1 ]]; then
    # Explicit binary path passed.
    BINARY="$1"
    ASSET_DIR="$SCRIPT_DIR"
elif [[ -n "$SCRIPT_DIR" && -f "$SCRIPT_DIR/../ventd" ]]; then
    # Running from scripts/ inside an extracted release tarball.
    BINARY="$SCRIPT_DIR/../ventd"
    ASSET_DIR="$SCRIPT_DIR"
elif [[ -n "$SCRIPT_DIR" && -f "./ventd" ]]; then
    # Running next to a locally-built binary (legacy flow).
    BINARY="./ventd"
    ASSET_DIR="$SCRIPT_DIR"
fi

if [[ -z "$BINARY" ]]; then
    # No local binary — fetch a release tarball.
    need_cmd() {
        command -v "$1" >/dev/null 2>&1 || {
            echo "error: required command not found: $1" >&2
            exit 1
        }
    }
    need_cmd curl
    need_cmd tar
    need_cmd uname
    need_cmd sha256sum

    # Architecture mapping.
    case "$(uname -m)" in
        x86_64|amd64)  ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *)
            echo "error: unsupported architecture: $(uname -m)" >&2
            echo "  Supported: amd64, arm64." >&2
            echo "  Build from source: https://github.com/${VENTD_REPO}" >&2
            exit 1
            ;;
    esac

    # Resolve version.
    if [[ -z "$VENTD_VERSION" ]]; then
        # Follow the /releases/latest redirect to discover the current tag.
        # GitHub responds with a Location header pointing at /releases/tag/<tag>.
        LATEST_URL="$(curl -sSLI -o /dev/null -w '%{url_effective}' \
            "https://github.com/${VENTD_REPO}/releases/latest")"
        VENTD_VERSION="${LATEST_URL##*/}"
        if [[ -z "$VENTD_VERSION" || "$VENTD_VERSION" == "releases" ]]; then
            echo "error: could not resolve latest release of ${VENTD_REPO}" >&2
            echo "  No published release found. Pin one with VENTD_VERSION=vX.Y.Z," >&2
            echo "  or build from source: https://github.com/${VENTD_REPO}" >&2
            exit 1
        fi
    fi

    # libc detection. The default release asset is glibc-linked (for NVML via
    # purego). musl distros (Alpine, Void-musl, etc.) need the `_musl` variant,
    # which is built with -tags nonvidia and is fully static.
    LIBC_SUFFIX=""
    if ls /lib/ld-musl-*.so.1 >/dev/null 2>&1 || ls /lib64/ld-musl-*.so.1 >/dev/null 2>&1; then
        LIBC_SUFFIX="_musl"
        echo "Detected musl libc — using nonvidia static build."
    fi

    # Tag is like v0.4.0; archive name uses the version without the leading v.
    VER="${VENTD_VERSION#v}"
    ARCHIVE="ventd_${VER}_linux_${ARCH}${LIBC_SUFFIX}.tar.gz"
    BASE_URL="https://github.com/${VENTD_REPO}/releases/download/${VENTD_VERSION}"

    TMPDIR_CLEANUP="$(mktemp -d)"
    echo "Downloading ventd ${VENTD_VERSION} (${ARCH})..."
    if ! curl -sSfL -o "${TMPDIR_CLEANUP}/${ARCHIVE}" "${BASE_URL}/${ARCHIVE}"; then
        echo "error: download failed: ${BASE_URL}/${ARCHIVE}" >&2
        exit 1
    fi
    if ! curl -sSfL -o "${TMPDIR_CLEANUP}/checksums.txt" "${BASE_URL}/checksums.txt"; then
        echo "error: checksum download failed: ${BASE_URL}/checksums.txt" >&2
        exit 1
    fi

    # Verify. BusyBox sha256sum (Alpine, embedded) lacks --ignore-missing,
    # so extract the expected hash for our archive and compare manually.
    echo "Verifying SHA-256..."
    EXPECTED_SHA="$(awk -v a="${ARCHIVE}" '$2 == a || $2 == "*"a {print $1; exit}' "${TMPDIR_CLEANUP}/checksums.txt")"
    if [[ -z "$EXPECTED_SHA" ]]; then
        echo "error: ${ARCHIVE} not listed in checksums.txt" >&2
        exit 1
    fi
    ACTUAL_SHA="$(sha256sum "${TMPDIR_CLEANUP}/${ARCHIVE}" | awk '{print $1}')"
    if [[ "$ACTUAL_SHA" != "$EXPECTED_SHA" ]]; then
        echo "error: checksum mismatch for ${ARCHIVE}" >&2
        echo "  expected: ${EXPECTED_SHA}" >&2
        echo "  actual:   ${ACTUAL_SHA}" >&2
        exit 1
    fi

    echo "Extracting..."
    tar -xzf "${TMPDIR_CLEANUP}/${ARCHIVE}" -C "$TMPDIR_CLEANUP"

    BINARY="${TMPDIR_CLEANUP}/ventd"
    ASSET_DIR="${TMPDIR_CLEANUP}/scripts"
    TARBALL_ROOT="${TMPDIR_CLEANUP}"

    if [[ ! -f "$BINARY" ]]; then
        echo "error: binary missing from archive: $BINARY" >&2
        exit 1
    fi
    chmod 755 "$BINARY"
fi

if [[ ! -f "$BINARY" ]]; then
    echo "error: binary not found: $BINARY" >&2
    if ls /lib/ld-musl-*.so.1 &>/dev/null 2>&1; then
        echo "  Build it with:  CGO_ENABLED=0 go build -tags nonvidia -o ventd ./cmd/ventd" >&2
    else
        echo "  Build it with:  go build -o ventd ./cmd/ventd" >&2
    fi
    exit 1
fi

# ── Service unit resolver ───────────────────────────────────────────────────
#
# Unit files live at different paths depending on source:
#   - local dev tree:     scripts/ventd.{openrc,runit} and deploy/ventd.service
#   - release tarball:    scripts/ventd.{openrc,runit} and deploy/ventd.service
#   - ad-hoc (arg flow):  same dir as install.sh
# Resolve each unit by trying known locations in order.

find_unit() {
    local name="$1"
    local candidates=()
    [[ -n "$ASSET_DIR" ]] && candidates+=("$ASSET_DIR/$name")
    [[ -n "${TARBALL_ROOT:-}" ]] && candidates+=("$TARBALL_ROOT/deploy/$name" "$TARBALL_ROOT/scripts/$name")
    [[ -n "$ASSET_DIR" ]] && candidates+=("$ASSET_DIR/../deploy/$name" "$ASSET_DIR/../scripts/$name")
    for c in "${candidates[@]}"; do
        if [[ -f "$c" ]]; then
            echo "$c"
            return 0
        fi
    done
    return 1
}

# ── Init system detection ────────────────────────────────────────────────────

detect_init() {
    if command -v systemctl &>/dev/null && [ -d /run/systemd/system ]; then
        echo "systemd"
    elif command -v rc-update &>/dev/null; then
        echo "openrc"
    elif command -v sv &>/dev/null; then
        echo "runit"
    else
        echo "unknown"
    fi
}

INIT_SYSTEM="${VENTD_INIT_SYSTEM:-$(detect_init)}"

case "$INIT_SYSTEM" in
    systemd)
        SERVICE_SRC="$(find_unit ventd.service)" || {
            echo "error: ventd.service not found" >&2
            exit 1
        }
        # The OnFailure recovery oneshot. Optional in the asset
        # tree (older release tarballs may not ship it) — if missing,
        # warn but continue; the daemon's graceful-exit path still
        # works, the operator just loses the SIGKILL/OOM safety net.
        RECOVER_SRC="$(find_unit ventd-recover.service 2>/dev/null)" || RECOVER_SRC=""
        ;;
    openrc)
        OPENRC_SRC="$(find_unit ventd.openrc)" || {
            echo "error: ventd.openrc not found" >&2
            exit 1
        }
        ;;
    runit)
        RUNIT_SRC="$(find_unit ventd.runit)" || {
            echo "error: ventd.runit not found" >&2
            exit 1
        }
        ;;
    unknown)
        echo "warning: no supported init system found (systemd, OpenRC, runit)."
        echo "  The binary will be installed but you will need to start ventd manually."
        ;;
esac

# ── Conflict preflight ───────────────────────────────────────────────────────
#
# Refuse to install if another daemon is already controlling fans or is bound
# to port 9999. Two daemons writing the same pwm<N> files race, and at least
# one will lose — leaving fans at whatever PWM the loser last wrote.

# Known fan-control daemons. Names cover systemd unit names and OpenRC
# service names; some projects ship under multiple names across distros.
FAN_DAEMON_CANDIDATES=(
    fancontrol
    fancontrold
    thinkfan
    nbfc
    nbfc_service
    nbfc-linux
    i8kmon
    dell-bios-fan-control
    asusd
    liquidctl
    coolercontrold
)

service_is_active() {
    local name="$1"
    case "$INIT_SYSTEM" in
        systemd)
            systemctl is-active --quiet "$name" 2>/dev/null
            ;;
        openrc)
            rc-service "$name" status 2>/dev/null | grep -qiE "started|running"
            ;;
        runit)
            [ -d "/var/service/$name" ] && sv status "$name" 2>/dev/null | grep -q "^run:"
            ;;
        *)
            return 1
            ;;
    esac
}

check_conflicting_daemon() {
    for svc in "${FAN_DAEMON_CANDIDATES[@]}"; do
        if service_is_active "$svc"; then
            echo "error: another fan-control daemon is running: $svc" >&2
            echo "" >&2
            echo "  Two daemons writing the same pwm files will race and your fans" >&2
            echo "  will end up at whatever PWM the loser last wrote. Stop and" >&2
            echo "  disable $svc before installing ventd:" >&2
            case "$INIT_SYSTEM" in
                systemd)
                    echo "    sudo systemctl disable --now $svc" >&2
                    ;;
                openrc)
                    echo "    sudo rc-service $svc stop" >&2
                    echo "    sudo rc-update del $svc" >&2
                    ;;
                runit)
                    echo "    sudo rm /var/service/$svc" >&2
                    ;;
            esac
            echo "" >&2
            echo "  Then re-run this installer." >&2
            exit 1
        fi
    done
}

# _pwm_holders_all_ventd — return 0 iff every PID in $1 (whitespace-separated,
# as fuser emits) belongs to a running ventd process (/proc/<pid>/comm ==
# "ventd"). Returns 1 if the list is empty or any PID is missing, unreadable,
# or has a different comm.
#
# Test hook: _VENTD_PROC_DIR overrides the procfs root so
# validation/install-pwm-holders.test.sh can exercise the carve-out without
# spawning real processes. See issue #107.
_pwm_holders_all_ventd() {
    local holders="$1"
    local proc_dir="${_VENTD_PROC_DIR:-/proc}"
    local any=0 pid comm
    for pid in $holders; do
        pid="${pid//[^0-9]/}"
        [[ -z "$pid" ]] && continue
        any=1
        if [[ ! -r "$proc_dir/$pid/comm" ]]; then
            return 1
        fi
        comm="$(tr -d '\n' <"$proc_dir/$pid/comm" 2>/dev/null || true)"
        [[ "$comm" == "ventd" ]] || return 1
    done
    (( any == 1 ))
}

check_pwm_holders() {
    # Best-effort: fuser on sysfs is unreliable on some kernels but catches
    # the common case where a fan daemon not in the list above has a pwm
    # file open for write.
    if ! command -v fuser >/dev/null 2>&1; then
        return 0
    fi
    local holders
    holders="$(fuser /sys/class/hwmon/*/pwm[0-9]* 2>/dev/null || true)"
    if [[ -z "$holders" ]]; then
        return 0
    fi
    # Upgrade carve-out (issue #107): if ventd is already running and every
    # PID holding a pwm file is itself a ventd process, the install step
    # below will try-restart it — don't error out on the upgrade path.
    # Mirrors the check_port_9999 precedent.
    if service_is_active "ventd" && _pwm_holders_all_ventd "$holders"; then
        return 0
    fi
    echo "error: another process is holding /sys/class/hwmon/*/pwm<N> open:" >&2
    echo "" >&2
    fuser -v /sys/class/hwmon/*/pwm[0-9]* 2>&1 | grep -vE '^\s*$' >&2 || true
    echo "" >&2
    echo "  This is almost always another fan-control daemon. Identify it" >&2
    echo "  from the PID list above and stop it before installing ventd." >&2
    exit 1
}

check_port_9999() {
    # If ventd is being reinstalled over itself, its own listener will be
    # on 9999 and that's fine — the install step below will restart it.
    # Skip the check if our own unit is the current owner.
    if service_is_active "ventd"; then
        return 0
    fi
    if command -v ss >/dev/null 2>&1; then
        local occupant
        occupant="$(ss -Hltnp 'sport = :9999' 2>/dev/null || true)"
        if [[ -n "$occupant" ]]; then
            echo "error: port 9999 is already bound:" >&2
            echo "" >&2
            echo "$occupant" >&2
            echo "" >&2
            echo "  ventd's web UI binds 127.0.0.1:9999 (localhost only) by default." >&2
            echo "  Stop the" >&2
            echo "  process above, or set web.listen to a different port in" >&2
            echo "  /etc/ventd/config.yaml before re-running this installer." >&2
            exit 1
        fi
    fi
    # ss-less fallback: best-effort via bash /dev/tcp. A successful connect
    # means something is listening; we don't know what, but we can refuse.
    if (exec 3<>/dev/tcp/127.0.0.1/9999) 2>/dev/null; then
        exec 3<&- 3>&-
        echo "error: port 9999 is already accepting connections on localhost." >&2
        echo "  Install ss (iproute2) to see what owns it, or stop the" >&2
        echo "  conflicting service before re-running this installer." >&2
        exit 1
    fi
}

if [[ "$VENTD_TEST_MODE" != "1" ]]; then
    echo "Checking for conflicting services..."
    check_conflicting_daemon
    check_pwm_holders
    check_port_9999
fi

# ── Install-environment preflight ──────────────────────────────────────────
#
# Before we install anything, verify the host can actually run ventd.
# Each check produces either nothing (silent pass), a warning the
# operator can ignore, or a hard error that aborts. Order matters:
# fatal blockers first, advisory checks last.

check_install_environment() {
    # 1. udevadm reachable. Without it the udev rule reload step is a
    #    no-op and pwm files stay root-owned until the next reboot.
    #    Treated as a warning, not a hard error — chrooted minimal
    #    environments may legitimately lack udevadm.
    if ! command -v udevadm >/dev/null 2>&1; then
        echo "  ! udevadm not found in PATH" >&2
        echo "    The shipped udev rule will only apply after the next reboot." >&2
    fi

    # 2. /etc/udev/rules.d writable. Read-only /etc layouts (some
    #    immutable distros, OSTree, container builds) cannot accept
    #    the rule and the daemon will be unable to write pwm files.
    if [[ ! -d /etc/udev/rules.d ]]; then
        # `install -d` later will try to create it; if /etc is RO the
        # create fails. Pre-flag here for a clearer error.
        if ! mkdir -p /etc/udev/rules.d 2>/dev/null; then
            echo "error: /etc/udev/rules.d is missing and /etc appears read-only" >&2
            echo "  ventd cannot ship its pwm group-write rule on this layout." >&2
            echo "  Immutable / OSTree distros need a different deployment path." >&2
            exit 1
        fi
        rmdir /etc/udev/rules.d 2>/dev/null || true
    fi

    # 3. SELinux enforcing on a system that hasn't been taught about
    #    ventd's pwm DAC will silently block pwm writes even after
    #    the udev rule chgrp's the file. Warn loudly so the operator
    #    knows where to look first if fans don't respond.
    if command -v getenforce >/dev/null 2>&1; then
        local sestate
        sestate="$(getenforce 2>/dev/null || true)"
        if [[ "$sestate" == "Enforcing" ]]; then
            echo "  ! SELinux is in Enforcing mode" >&2
            echo "    ventd's pwm writes happen via DAC; if SELinux denies them," >&2
            echo "    audit2allow on the AVC denials and ship a custom policy." >&2
            echo "    The installer cannot do this for you — distro-specific." >&2
        fi
    fi

    # 4. AppArmor enforcement on the kernel module class for the
    #    daemon binary. If a profile already restricts /usr/local/bin/ventd
    #    or /usr/bin/ventd, the udev-granted DAC may be overridden. Warn.
    if command -v aa-status >/dev/null 2>&1; then
        if aa-status --enabled 2>/dev/null; then
            local profiles
            profiles="$(aa-status --profiled 2>/dev/null | grep -E 'ventd|hwmon' || true)"
            if [[ -n "$profiles" ]]; then
                echo "  ! AppArmor profile referencing ventd or hwmon is active" >&2
                echo "    Verify the profile permits write to /sys/class/hwmon/*/pwm*" >&2
                echo "    for the ventd group; if not, ventd will fail to write PWM." >&2
            fi
        fi
    fi

    # 5. /sys/class/hwmon visible at all. Some hardened/minimal kernels
    #    disable CONFIG_HWMON or boot with sysfs hidden. ventd has
    #    nothing to do on such a system; abort early with a clear error.
    if [[ ! -d /sys/class/hwmon ]]; then
        echo "error: /sys/class/hwmon does not exist on this kernel" >&2
        echo "  This kernel does not expose hwmon (CONFIG_HWMON=n or sysfs hidden)." >&2
        echo "  ventd cannot control fans without it; aborting." >&2
        exit 1
    fi
}

if [[ "$VENTD_TEST_MODE" != "1" ]]; then
    echo "Running install-environment preflight..."
    check_install_environment
fi

# ── ventd account ────────────────────────────────────────────────────────────
#
# The daemon runs as the unprivileged "ventd" user/group. Access to pwm
# sysfs files comes from DAC (group ownership applied by the udev rule
# installed below) — not from capabilities. CapabilityBoundingSet= stays
# empty in the unit. Account creation lives in _ventd_account.sh so
# this script and the .deb/.rpm postinstall hook use one copy.

ACCOUNT_HELPER=""
for candidate in \
    "${ASSET_DIR}/_ventd_account.sh" \
    "${ASSET_DIR}/../scripts/_ventd_account.sh" \
    "${TARBALL_ROOT:-}/scripts/_ventd_account.sh"; do
    if [[ -n "$candidate" && -f "$candidate" ]]; then
        ACCOUNT_HELPER="$candidate"
        break
    fi
done

if [[ -z "$ACCOUNT_HELPER" ]]; then
    echo "error: scripts/_ventd_account.sh not found — installer cannot create ventd account" >&2
    exit 1
fi

# shellcheck source=scripts/_ventd_account.sh
. "$ACCOUNT_HELPER"

if [[ "$VENTD_TEST_MODE" != "1" ]]; then
    echo "Ensuring ventd system account exists..."
    ventd_create_account
fi

# _ventd_add_nvidia_group — add the ventd user to the group that owns the
# NVIDIA control device so NVML can open it without elevated privileges.
#
# /dev/nvidiactl is typically owned root:video on Ubuntu/Debian; some distros
# use render or a distro-specific group. We stat the actual device node rather
# than hard-coding "video" so the installer works across distros.
#
# Test hook: _VENTD_NVIDIACTL_PATH overrides the default /dev/nvidiactl so
# validation/install-nvidia.test.sh can point at a mock device node.
_ventd_add_nvidia_group() {
    local ctl_path="${_VENTD_NVIDIACTL_PATH:-/dev/nvidiactl}"
    if [ ! -e "$ctl_path" ]; then
        return 0  # no NVIDIA device; nothing to do
    fi
    local nvidia_group
    nvidia_group="$(stat -c '%G' "$ctl_path" 2>/dev/null || true)"
    if [ -z "$nvidia_group" ] || [ "$nvidia_group" = "root" ]; then
        return 0  # root-owned device doesn't need a group membership fix
    fi
    if id ventd 2>/dev/null | tr ',' '\n' | grep -q "(${nvidia_group})"; then
        echo "  ✓ ventd user already in ${nvidia_group} group (NVML access confirmed)"
        return 0
    fi
    usermod -aG "$nvidia_group" ventd
    echo "  ✓ Added ventd user to ${nvidia_group} group for NVML access"
}

if [[ "$VENTD_TEST_MODE" != "1" ]]; then
    _ventd_add_nvidia_group
fi

# ── Install ──────────────────────────────────────────────────────────────────

echo "Installing ventd..."

install -d -m 755 "$VENTD_PREFIX"
install -m 755 "$BINARY" "$VENTD_PREFIX/ventd"
echo "  ✓ binary → $VENTD_PREFIX/ventd"

# ── Preflight ────────────────────────────────────────────────────────────────
#
# Run the preflight orchestrator now, before any systemd unit is laid down.
# Detects pre-calibration blockers (Secure Boot prerequisites, missing
# kernel headers, in-tree driver conflicts, stale DKMS state, competing
# userspace fan daemons, etc.) and — when running on a TTY — walks the
# operator through Y/N-gated auto-fixes.
#
# TTY detection preserves the curl-pipe-bash one-liner: piped form
# (`curl -sSL .../install.sh | sudo bash`) gets the JSON+hard-exit path
# with a re-entry hint; file-form (`sudo bash <(curl ...)` or
# `sudo ./install.sh`) gets the interactive Y/N flow.
#
# Exit code mapping:
#   0 — preflight clean, continue install
#   1 — internal error (broken binary, etc.) — abort
#   2 — operator declined a blocker auto-fix or fix failed — abort
#   3 — fix queued, reboot requested — exit cleanly so operator can reboot
#       and re-run install.sh (which will resume from this point)
if [[ "${VENTD_SKIP_PREFLIGHT:-0}" != "1" ]]; then
    echo
    echo "Running install-time preflight..."
    if [[ -t 0 ]]; then
        "$VENTD_PREFIX/ventd" preflight --interactive
        rc=$?
    else
        # Piped form: cannot prompt. Run JSON detect-only; if any
        # blocker is found, print the human-readable summary and
        # the actionable re-entry command, then exit 1.
        if ! "$VENTD_PREFIX/ventd" preflight --json >/tmp/ventd-preflight.json 2>/dev/null; then
            echo
            "$VENTD_PREFIX/ventd" preflight 2>/dev/null || true
            echo
            echo "Preflight blockers detected. Curl-pipe-bash cannot prompt for"
            echo "auto-fix consent — re-run from a real terminal:"
            echo
            echo "    sudo bash <(curl -sSL https://github.com/ventd/ventd/releases/latest/download/install.sh)"
            echo
            exit 1
        fi
        rc=0
    fi
    case "$rc" in
        0)
            echo "  ✓ preflight clean"
            ;;
        2)
            echo
            echo "Preflight blockers remain unresolved. Aborting install." >&2
            echo "Re-run \`sudo $VENTD_PREFIX/ventd preflight --interactive\` to retry." >&2
            exit 1
            ;;
        3)
            echo
            echo "A reboot is required to complete the preflight (likely MOK enrollment)."
            echo "After rebooting and confirming the MOK in the firmware screen, re-run:"
            echo
            echo "    sudo bash <(curl -sSL https://github.com/ventd/ventd/releases/latest/download/install.sh)"
            echo
            exit 0
            ;;
        *)
            echo "Preflight failed (exit $rc). Aborting install." >&2
            exit 1
            ;;
    esac
fi

# ventd-wait-hwmon: ExecStartPre gate for the cold-boot udev race
# (issue #103). Lives under /usr/local/sbin because it's a root-only
# systemd helper; operators never run it by hand. Only referenced by
# deploy/ventd.service — openrc/runit installs ship the script too
# so a later init-system switch works without a reinstall, but their
# service wrappers don't invoke it.
WAIT_HWMON_SRC=""
for candidate in \
    "${ASSET_DIR}/ventd-wait-hwmon" \
    "${ASSET_DIR}/../scripts/ventd-wait-hwmon" \
    "${TARBALL_ROOT:-}/scripts/ventd-wait-hwmon"; do
    if [[ -n "$candidate" && -f "$candidate" ]]; then
        WAIT_HWMON_SRC="$candidate"
        break
    fi
done
if [[ -n "$WAIT_HWMON_SRC" ]]; then
    install -d -m 755 "$VENTD_SBIN_DIR"
    install -m 755 "$WAIT_HWMON_SRC" "$VENTD_SBIN_DIR/ventd-wait-hwmon"
    echo "  ✓ wait-hwmon helper → $VENTD_SBIN_DIR/ventd-wait-hwmon"
else
    echo "  ! ventd-wait-hwmon not found in asset tree — cold-boot race"
    echo "    will rely on in-binary retry alone (still correct, one layer)"
fi

# ventd-recover: emergency pwm_enable restore binary. Installed to
# /usr/local/sbin so ventd-recover.service can call it as root outside
# the main daemon's sandbox. Optional in older release tarballs — if
# absent, graceful-exit watchdog still works; only SIGKILL/OOM recovery
# is missing.
# VENTD_RECOVER_BIN may be set by test suites to inject a pre-built binary
# path directly without modifying the source tree.
RECOVER_BIN_SRC="${VENTD_RECOVER_BIN:-}"
if [[ -z "$RECOVER_BIN_SRC" ]]; then
    for candidate in \
        "${ASSET_DIR}/ventd-recover" \
        "${ASSET_DIR}/../ventd-recover" \
        "${TARBALL_ROOT:-}/ventd-recover"; do
        if [[ -n "$candidate" && -f "$candidate" ]]; then
            RECOVER_BIN_SRC="$candidate"
            break
        fi
    done
fi
if [[ -n "$RECOVER_BIN_SRC" ]]; then
    install -d -m 755 "$VENTD_SBIN_DIR"
    install -m 755 "$RECOVER_BIN_SRC" "$VENTD_SBIN_DIR/ventd-recover"
    echo "  ✓ recovery binary → $VENTD_SBIN_DIR/ventd-recover"
else
    echo "  ! ventd-recover binary not found in asset tree — SIGKILL/OOM"
    echo "    fan restore unavailable; graceful-exit watchdog still active"
fi

# /etc/ventd is group-readable (0750) so the ventd group (daemon only)
# can read config while "other" stays locked out. On systemd,
# ConfigurationDirectory= reasserts this on every start; the install
# here is belt-and-braces for the wipe-and-reinstall path.
#
# chown is recursive: on upgrade from a prior User=root install the
# directory already holds config.yaml / calibration.json / TLS
# cert+key owned by root:root. Without -R those survive the upgrade
# root-owned, the daemon starts as User=ventd, and config reads hit
# EACCES. Mirrors the recursive chown in scripts/postinstall.sh for
# the .deb/.rpm path.
install -d -m 0750 "$VENTD_ETC_DIR"
if [[ "$VENTD_TEST_MODE" != "1" ]]; then
    chown -R ventd:ventd "$VENTD_ETC_DIR"
fi

# /var/lib/ventd — persistent state: calibration store, observation log.
# StateDirectory=ventd in the unit reasserts ownership/mode on every start;
# this is belt-and-braces for the wipe-and-reinstall path.
# /run/ventd is managed by RuntimeDirectory= and must NOT be pre-created here.
install -d -m 0750 "$VENTD_STATE_DIR"
if [[ "$VENTD_TEST_MODE" != "1" ]]; then
    chown ventd:ventd "$VENTD_STATE_DIR"
fi

case "$INIT_SYSTEM" in

    systemd)
        # Refresh unit files on every run. install(1) overwrites whatever is
        # already there, so an upgrade that changed deploy/ventd.service (e.g.
        # issue #58's OnFailure= move) takes effect immediately after the
        # daemon-reload + restart below. Hash-compare first so we can log a
        # single diagnostic when the on-disk units actually changed.
        install -d -m 0755 "$VENTD_SYSTEMD_DIR"
        SERVICE_DST="$VENTD_SYSTEMD_DIR/ventd.service"
        RECOVER_DST="$VENTD_SYSTEMD_DIR/ventd-recover.service"

        UNIT_CHANGED=0
        if [[ ! -f "$SERVICE_DST" ]] || ! cmp -s "$SERVICE_SRC" "$SERVICE_DST"; then
            UNIT_CHANGED=1
        fi
        if [[ -n "$RECOVER_SRC" ]]; then
            if [[ ! -f "$RECOVER_DST" ]] || ! cmp -s "$RECOVER_SRC" "$RECOVER_DST"; then
                UNIT_CHANGED=1
            fi
        fi

        # Record whether the service was already active before the refresh.
        # If it was, the new unit doesn't take effect until try-restart —
        # systemctl start is a no-op against an already-active unit and
        # leaves the daemon running the stale version.
        WAS_ACTIVE=0
        if [[ "$VENTD_TEST_MODE" != "1" ]]; then
            if systemctl is-active --quiet ventd.service 2>/dev/null; then
                WAS_ACTIVE=1
            fi
        fi

        install -m 0644 "$SERVICE_SRC" "$SERVICE_DST"
        if [[ -n "$RECOVER_SRC" ]]; then
            install -m 0644 "$RECOVER_SRC" "$RECOVER_DST"
            echo "  ✓ systemd unit → $RECOVER_DST (OnFailure helper)"
        else
            echo "  ! ventd-recover.service not found in asset tree — SIGKILL/OOM"
            echo "    safety net unavailable; graceful-exit watchdog still active"
        fi

        # Post-reboot verifier (issue #111). Opt-in via
        # VENTD_INSTALL_POSTREBOOT_VERIFY=1. When set, the one-shot
        # service and its helper script land on disk here and the unit
        # is enabled below so it fires once on the next boot, writing
        # PASS/FAIL to /var/log/ventd/postreboot-<TS>.log.
        VERIFY_ENABLE=0
        if [[ "${VENTD_INSTALL_POSTREBOOT_VERIFY:-0}" == "1" ]]; then
            VERIFY_UNIT_SRC="$(find_unit ventd-postreboot-verify.service 2>/dev/null)" || VERIFY_UNIT_SRC=""
            VERIFY_SCRIPT_SRC=""
            for candidate in \
                "${ASSET_DIR}/postreboot-verify.sh" \
                "${ASSET_DIR}/../deploy/postreboot-verify.sh" \
                "${TARBALL_ROOT:-}/deploy/postreboot-verify.sh"; do
                if [[ -n "$candidate" && -f "$candidate" ]]; then
                    VERIFY_SCRIPT_SRC="$candidate"
                    break
                fi
            done
            if [[ -n "$VERIFY_UNIT_SRC" && -n "$VERIFY_SCRIPT_SRC" ]]; then
                install -d -m 755 "$VENTD_SBIN_DIR"
                install -m 0755 "$VERIFY_SCRIPT_SRC" "$VENTD_SBIN_DIR/ventd-postreboot-verify.sh"
                install -m 0644 "$VERIFY_UNIT_SRC" "$VENTD_SYSTEMD_DIR/ventd-postreboot-verify.service"
                echo "  ✓ post-reboot verifier → $VENTD_SBIN_DIR/ventd-postreboot-verify.sh"
                echo "  ✓ post-reboot verifier unit → $VENTD_SYSTEMD_DIR/ventd-postreboot-verify.service"
                VERIFY_ENABLE=1
            else
                echo "  ! VENTD_INSTALL_POSTREBOOT_VERIFY=1 but verifier assets not found — skipped"
            fi
        fi

        if [[ "$VENTD_TEST_MODE" != "1" ]]; then
            systemctl daemon-reload
            if (( WAS_ACTIVE == 1 )); then
                # Defer try-restart until AppArmor / udev / module probe
                # have run; otherwise the running daemon picks up the new
                # unit before its prerequisites are in place.
                echo "  ✓ systemd unit → $SERVICE_DST (reloaded; restart deferred)"
            else
                systemctl enable ventd.service
                # Defer start until AppArmor / udev / module probe have
                # run. The unit pins AppArmorProfile=ventd, which is a
                # hard fail (status=231/APPARMOR) if the kernel has no
                # such profile loaded. See the deferred-start block at
                # the end of this script.
                echo "  ✓ systemd unit → $SERVICE_DST (enabled; start deferred)"
            fi
            if [[ -n "$RECOVER_SRC" ]]; then
                systemctl enable ventd-recover.service
                echo "  ✓ ventd-recover.service enabled (OnFailure hook registered)"
            fi
            if (( VERIFY_ENABLE == 1 )); then
                systemctl enable ventd-postreboot-verify.service
                echo "  ✓ post-reboot verifier enabled (fires on next boot)"
            fi
        else
            echo "  ✓ systemd unit → $SERVICE_DST (test mode — systemctl skipped)"
        fi

        if (( UNIT_CHANGED == 1 )); then
            echo "  ✓ unit files updated"
        fi
        ;;

    openrc)
        install -m 755 "$OPENRC_SRC" /etc/init.d/ventd
        rc-update add ventd default
        # Defer rc-service start until prerequisites (udev, modules,
        # apparmor) are in place. See the deferred-start block at
        # the end of this script.
        echo "  ✓ OpenRC init script → /etc/init.d/ventd (enabled; start deferred)"
        ;;

    runit)
        install -d -m 755 /etc/sv/ventd
        install -d -m 755 /etc/sv/ventd/log
        install -m 755 "$RUNIT_SRC" /etc/sv/ventd/run

        install -d -m 755 /var/log/ventd
        cat > /etc/sv/ventd/log/run <<'EOF'
#!/bin/sh
exec svlogd -tt /var/log/ventd
EOF
        chmod 755 /etc/sv/ventd/log/run

        # The symlink into the runsv dir starts the service immediately
        # because runsvdir polls for new entries. Defer it until
        # prerequisites (apparmor profile, udev rules, kernel modules)
        # are installed — see the deferred-start block at the end.
        echo "  ✓ runit service → /etc/sv/ventd (start deferred)"
        ;;

    unknown)
        echo "  ! no init system detected — service not registered"
        ;;
esac

# ── udev rule for pwm group access ──────────────────────────────────────────
#
# ventd runs as the unprivileged "ventd" user; it can only write pwm<N>
# and pwm<N>_enable if the kernel grants it via DAC. The shipped rule
# is chip-agnostic — it fires on every hwmon device and chgrp's
# whatever pwm<N> / pwm<N>_enable files the chip exposes (if any)
# to the ventd group with g+w. Chips without pwm files are traversed
# harmlessly. See deploy/90-ventd-hwmon.rules for the full design note.

UDEV_RULE_SRC=""
for candidate in \
    "${ASSET_DIR}/90-ventd-hwmon.rules" \
    "${ASSET_DIR}/../deploy/90-ventd-hwmon.rules" \
    "${TARBALL_ROOT:-}/deploy/90-ventd-hwmon.rules"; do
    if [[ -n "$candidate" && -f "$candidate" ]]; then
        UDEV_RULE_SRC="$candidate"
        break
    fi
done

if [[ "$VENTD_TEST_MODE" != "1" ]]; then
    if [[ -n "$UDEV_RULE_SRC" ]]; then
        install -d -m 755 /etc/udev/rules.d
        install -m 644 "$UDEV_RULE_SRC" /etc/udev/rules.d/90-ventd-hwmon.rules
        echo "  ✓ udev rule → /etc/udev/rules.d/90-ventd-hwmon.rules"
        if command -v udevadm >/dev/null 2>&1; then
            udevadm control --reload >/dev/null 2>&1 || true
            udevadm trigger --subsystem-match=hwmon >/dev/null 2>&1 || true
        fi
    else
        echo "  ! udev rule template not found — skipping"
        echo "    (pwm writes will fail until /sys/class/hwmon/*/pwm* are g+w for the ventd group)"
    fi
fi

# ── Probe + persist hwmon kernel modules ────────────────────────────────────
#
# The daemon runs under ProtectKernelModules=yes (deny init_module /
# finit_module) and ProtectSystem=strict (read-only /etc), so it cannot
# modprobe or write to /etc/modules-load.d. Both operations have to
# happen here, while we still hold root and live outside any sandbox.
#
# `ventd --probe-modules` runs the install-time module probe:
#   1. install lm-sensors via the host package manager if missing
#   2. run sensors-detect --auto and load recommended modules
#   3. fall back to a kernel-module enumeration on miss
#   4. write /etc/modules-load.d/ventd.conf so systemd-modules-load
#      picks the winning module up on every subsequent boot
#   5. write /etc/modprobe.d/ventd.conf for any required force_id args
#
# The exit code is best-effort: a probe failure (no internet for
# lm-sensors fetch, hostile network, kmod-blocked container) is logged
# but not fatal. The daemon's startup runs DiagnoseHwmon (read-only)
# and surfaces a clear remediation pointer when no PWM is visible, so
# operators see the issue immediately on first start.

if [[ "$VENTD_TEST_MODE" != "1" ]]; then
    echo "Probing hwmon kernel modules (one-shot)..."
    if "$VENTD_PREFIX/ventd" --probe-modules >/dev/null 2>&1; then
        echo "  ✓ hwmon module probe complete"
    else
        echo "  ! hwmon module probe returned non-zero — daemon will diagnose at startup"
    fi
fi

# ── Optional MAC policy install ──────────────────────────────────────────────
#
# AppArmor and SELinux ship as separate concerns: the policy files
# live in deploy/apparmor.d/ and deploy/selinux/. Install them only
# when the corresponding kernel-side enforcement is present so we
# don't drop policy on systems that won't use it.

# log_security_outcome appends one timestamped line to /var/log/ventd/install.log
# so a silent confinement downgrade is still auditable once the install
# scrollback is gone. The directory is created on first call with mode
# 0750 and owned root:ventd — the ventd group is created earlier by the
# account helper. Best-effort: any mkdir / chown / write failure is
# non-fatal; this is a signalling enhancement, not a gating one. See #211.
log_security_outcome() {
    local module="$1" outcome="$2" detail="$3"
    [[ "$VENTD_TEST_MODE" == "1" ]] && return 0
    install -d -m 750 /var/log/ventd 2>/dev/null || return 0
    if getent group ventd >/dev/null 2>&1; then
        chown root:ventd /var/log/ventd 2>/dev/null || true
    fi
    local ts
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date)"
    printf '%s %s=%s %s\n' "$ts" "$module" "$outcome" "$detail" \
        >> /var/log/ventd/install.log 2>/dev/null || true
    chmod 640 /var/log/ventd/install.log 2>/dev/null || true
    if getent group ventd >/dev/null 2>&1; then
        chown root:ventd /var/log/ventd/install.log 2>/dev/null || true
    fi
}

install_apparmor_profile() {
    if ! command -v apparmor_parser >/dev/null 2>&1; then
        log_security_outcome apparmor skipped "reason=parser-not-installed"
        return 0
    fi
    local profiles_dir=""
    for candidate in \
        "${ASSET_DIR}/../deploy/apparmor.d" \
        "${TARBALL_ROOT:-}/deploy/apparmor.d"; do
        if [[ -n "$candidate" && -d "$candidate" ]]; then
            profiles_dir="$candidate"
            break
        fi
    done
    if [[ -z "$profiles_dir" ]]; then
        log_security_outcome apparmor skipped "reason=profile-not-shipped"
        return 0
    fi
    install -d -m 755 /etc/apparmor.d
    local any_loaded=0
    for profile in ventd ventd-ipmi; do
        local src="${profiles_dir}/${profile}"
        if [[ ! -f "$src" ]]; then
            continue
        fi
        install -m 644 "$src" "/etc/apparmor.d/${profile}"
        local parser_rc=0
        apparmor_parser -r "/etc/apparmor.d/${profile}" 2>/dev/null || parser_rc=$?
        if [[ $parser_rc -eq 0 ]]; then
            echo "  ✓ AppArmor profile → /etc/apparmor.d/${profile} (loaded, enforce mode)"
            log_security_outcome apparmor loaded "profile=/etc/apparmor.d/${profile} mode=enforce"
            any_loaded=1
        else
            echo "  ! AppArmor profile installed but parser refused to load it"
            echo "    (run \`apparmor_parser -r /etc/apparmor.d/${profile}\` for details)"
            log_security_outcome apparmor refused "parser_exit=${parser_rc} profile=/etc/apparmor.d/${profile}"
        fi
    done
    # On hosts with Docker installed, docker-default can win the AppArmor
    # attachment race for /usr/local/bin/* binaries. Explicitly enforce.
    if [[ $any_loaded -eq 1 ]] && systemctl is-active --quiet docker 2>/dev/null \
        && command -v aa-enforce >/dev/null 2>&1; then
        aa-enforce /etc/apparmor.d/ventd 2>/dev/null || true
        echo "  ✓ ventd AppArmor profile enforced (docker detected)"
    fi
}

install_selinux_module() {
    if ! command -v semodule >/dev/null 2>&1 || ! command -v checkmodule >/dev/null 2>&1; then
        log_security_outcome selinux skipped "reason=tools-not-installed"
        return 0
    fi
    local srcdir=""
    for candidate in \
        "${ASSET_DIR}/../deploy/selinux" \
        "${TARBALL_ROOT:-}/deploy/selinux"; do
        if [[ -n "$candidate" && -d "$candidate" ]]; then
            srcdir="$candidate"
            break
        fi
    done
    if [[ -z "$srcdir" ]]; then
        log_security_outcome selinux skipped "reason=module-not-shipped"
        return 0
    fi
    if [[ ! -d /usr/share/selinux/devel ]]; then
        echo "  ! SELinux tooling present but selinux-policy-devel is missing"
        echo "    Install it (selinux-policy-devel on Fedora/RHEL, selinux-policy-dev"
        echo "    on Debian/Ubuntu) then run: sudo make -C ${srcdir} install"
        log_security_outcome selinux skipped "reason=selinux-policy-devel-missing"
        return 0
    fi
    local builddir
    builddir="$(mktemp -d)"
    cp "${srcdir}"/ventd.te "${srcdir}"/ventd.fc "${builddir}/" || {
        log_security_outcome selinux refused "reason=source-copy-failed"
        rm -rf "$builddir"
        return 0
    }
    if ( cd "$builddir" && make -f /usr/share/selinux/devel/Makefile ventd.pp >/dev/null 2>&1 ); then
        if semodule -i "${builddir}/ventd.pp" 2>/dev/null; then
            restorecon -Rv /usr/local/bin/ventd /etc/ventd /run/ventd >/dev/null 2>&1 || true
            echo "  ✓ SELinux module → ventd.pp (loaded; restorecon applied)"
            log_security_outcome selinux loaded "module=ventd.pp"
        else
            echo "  ! SELinux module built but semodule refused to load it"
            log_security_outcome selinux refused "reason=semodule-refused module=ventd.pp"
        fi
    else
        echo "  ! SELinux module build failed (run make in ${srcdir} for details)"
        log_security_outcome selinux refused "reason=make-failed"
    fi
    rm -rf "$builddir"
}

if [[ "$VENTD_TEST_MODE" != "1" ]]; then
    install_apparmor_profile
    install_selinux_module
fi

# ── Deferred service start ──────────────────────────────────────────────────
#
# The earlier init-system block enabled (or installed unit files for) the
# service but deferred the actual start. We waited so that AppArmor
# profiles, udev rules, and kernel modules are all in place before the
# kernel evaluates the unit's AppArmorProfile=, before pwm DAC group
# bits are needed, and before the daemon enumerates hwmon. Without
# this defer, ventd.service exits 231/APPARMOR on the first start
# whenever apparmor_parser is present but the profile hasn't been
# loaded yet. See issue #695.

if [[ "$VENTD_TEST_MODE" != "1" ]]; then
    case "$INIT_SYSTEM" in
        systemd)
            if (( WAS_ACTIVE == 1 )); then
                systemctl try-restart ventd.service || true
                echo "  ✓ ventd.service restarted (prerequisites in place)"
            else
                systemctl start ventd.service || true
                echo "  ✓ ventd.service started"
            fi
            ;;
        openrc)
            rc-service ventd start || true
            echo "  ✓ ventd started via OpenRC"
            ;;
        runit)
            if [ -d /var/service ]; then
                ln -sfn /etc/sv/ventd /var/service/ventd
                echo "  ✓ runit service linked in /var/service (auto-starts)"
            elif [ -d /etc/runit/runsvdir/default ]; then
                ln -sfn /etc/sv/ventd /etc/runit/runsvdir/default/ventd
                echo "  ✓ runit service linked in /etc/runit/runsvdir/default (auto-starts)"
            else
                echo "  ! could not find runit service directory; link manually:"
                echo "      ln -s /etc/sv/ventd /var/service/ventd"
            fi
            ;;
    esac
fi

# ── Post-start verification ─────────────────────────────────────────────────
#
# Give the service a few seconds to settle, then confirm it's actually up.
# Catches binary-exec failures (wrong libc, missing loader) that manifest
# as an immediate restart-loop rather than a hard install error above.

verify_running() {
    case "$INIT_SYSTEM" in
        systemd)
            systemctl is-active --quiet ventd.service
            ;;
        openrc)
            rc-service ventd status 2>/dev/null | grep -qiE "started|running"
            ;;
        runit)
            sv status ventd 2>/dev/null | grep -q "^run:"
            ;;
        *)
            # No init system, nothing to verify.
            return 0
            ;;
    esac
}

if [[ "$INIT_SYSTEM" != "unknown" && "$VENTD_TEST_MODE" != "1" ]]; then
    sleep 3
    if ! verify_running; then
        echo ""
        echo "error: ventd was installed but is not running." >&2
        case "$INIT_SYSTEM" in
            systemd) echo "  Inspect the log:  journalctl -u ventd -n 50 --no-pager" >&2 ;;
            openrc)  echo "  Inspect the log:  tail -n 50 /var/log/ventd.log 2>/dev/null || rc-service ventd status" >&2 ;;
            runit)   echo "  Inspect the log:  tail -n 50 /var/log/ventd/current" >&2 ;;
        esac
        exit 1
    fi
fi

# ── NVML post-install verification ──────────────────────────────────────────
#
# If an NVIDIA GPU is detected, verify that the ventd user can reach NVML via
# nvidia-smi. Catches group-membership gaps on systems where usermod took
# effect in the process table but the service user's credential still caches
# the old groups (a daemon restart flushes this — the check also nudges the
# operator if they need to re-login for interactive nvidia-smi calls).

if [[ "$INIT_SYSTEM" != "unknown" && "$VENTD_TEST_MODE" != "1" && -e /dev/nvidiactl ]]; then
    if command -v nvidia-smi >/dev/null 2>&1; then
        if sudo -u ventd nvidia-smi -q -d PIDS 2>&1 | grep -q "NVIDIA-SMI"; then
            echo "  ✓ NVML accessible from ventd user"
        else
            echo "  ⚠ NVML verification failed. GPU features may be disabled."
            echo "    Check: sudo -u ventd nvidia-smi"
            echo "    Typical fix: sudo usermod -aG video ventd && sudo systemctl restart ventd"
        fi
    fi
fi

# ── Done ─────────────────────────────────────────────────────────────────────

# Resolve a machine IP for the "open https://… to set up" hint. `hostname -I`
# is a GNU-hostname extension not present in inetutils-hostname (Arch's
# default) — under `set -o pipefail` that would make the install script
# exit non-zero right after a perfectly healthy install and the operator
# never sees the URL. Fall back through ip(8), and leave a placeholder
# if neither resolves. Best-effort: any failure here is informational only.
MACHINE_IP="$(hostname -I 2>/dev/null | awk '{print $1}')" || MACHINE_IP=""
if [[ -z "$MACHINE_IP" ]] && command -v ip >/dev/null 2>&1; then
    MACHINE_IP="$(ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1)" || MACHINE_IP=""
fi
# Scheme is always https on first boot: the daemon auto-generates a
# self-signed cert if no tls_cert is configured. Older installs that
# explicitly disabled TLS in config.yaml are the only reason this would
# drop to http — that case is extremely rare and the wizard itself
# surfaces the right URL anyway, so we bias toward the correct default.
WEB_SCHEME="https"
WEB_URL="${WEB_SCHEME}://${MACHINE_IP:-<this-machine-ip>}:9999"

# Compute the SHA-256 fingerprint of the daemon's self-signed cert. The
# operator can compare this to the certificate fingerprint shown by their
# browser when accepting the security warning, confirming the cert was
# generated by this machine and not a MITM on the LAN. Best-effort: any
# parse failure leaves CERT_FINGERPRINT empty and the box prints generic
# guidance.
CERT_FINGERPRINT=""
CERT_PATH="$VENTD_ETC_DIR/tls.crt"
if [[ "$VENTD_TEST_MODE" != "1" && -r "$CERT_PATH" ]] && command -v openssl >/dev/null 2>&1; then
    for _ in 1 2 3 4 5; do
        if [[ -s "$CERT_PATH" ]]; then
            CERT_FINGERPRINT="$(openssl x509 -in "$CERT_PATH" -noout -fingerprint -sha256 2>/dev/null \
                | sed -e 's/^.*Fingerprint=//' -e 's/^sha256 //I' || true)"
            [[ -n "$CERT_FINGERPRINT" ]] && break
        fi
        sleep 1
    done
fi

# Visually distinct completion block. Box-drawn so the URL + fingerprint
# don't disappear into the scrollback of a noisy apt-get / dnf / pacman
# run. The characters below are Unicode box-drawing; they render correctly
# on every terminal the README lists as a supported install surface.
echo ""
cat <<EOF

╔════════════════════════════════════════════════════════════════════╗
║  ventd is installed and running.                                   ║
╠════════════════════════════════════════════════════════════════════╣
║                                                                    ║
║    Open this URL in your browser:                                  ║
║                                                                    ║
║         ${WEB_URL}
║                                                                    ║
║    Set a password on first visit — that's it. No more terminal     ║
║    work required.                                                  ║
║                                                                    ║
╠════════════════════════════════════════════════════════════════════╣
║  About the security warning                                        ║
╠════════════════════════════════════════════════════════════════════╣
║                                                                    ║
║    Your browser will warn you about an unsafe certificate. This    ║
║    is expected: ventd generated a self-signed certificate on this  ║
║    machine to encrypt the connection to your LAN. The warning is   ║
║    your browser saying "I don't recognise the issuer" — not        ║
║    "this connection is broken".                                    ║
║                                                                    ║
║    Chrome / Edge / Brave: click *Advanced* → *Proceed*.            ║
║    Firefox:               click *Advanced* → *Accept the Risk*.    ║
║    Safari:                click *Show Details* → *visit website*.  ║
║                                                                    ║
EOF

if [[ -n "$CERT_FINGERPRINT" ]]; then
    cat <<EOF
║    To verify you're talking to *this* machine and not someone      ║
║    intercepting your connection, check that the SHA-256            ║
║    fingerprint shown in your browser's certificate dialog          ║
║    matches:                                                        ║
║                                                                    ║
║      ${CERT_FINGERPRINT}
║                                                                    ║
EOF
else
    cat <<EOF
║    To verify the certificate fingerprint, run on this machine:     ║
║                                                                    ║
║      sudo openssl x509 -in ${CERT_PATH} \\
║        -noout -fingerprint -sha256                                 ║
║                                                                    ║
║    and compare against the fingerprint shown in your browser's     ║
║    certificate dialog.                                             ║
║                                                                    ║
EOF
fi

cat <<EOF
╚════════════════════════════════════════════════════════════════════╝

EOF

if [[ "$INIT_SYSTEM" == "unknown" ]]; then
    echo ""
    echo "(No supported init system detected. Start the daemon manually"
    echo "   as the ventd service user so files under /etc/ventd stay"
    echo "   owned by ventd:ventd:"
    echo "   sudo -u ventd $VENTD_PREFIX/ventd --config /etc/ventd/config.yaml)"
fi
