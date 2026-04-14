#!/usr/bin/env bash
# ventd install script
#
# Usage:
#   curl -sSL https://raw.githubusercontent.com/ventd/ventd/main/scripts/install.sh | sudo bash
#   sudo ./install.sh                         # from an extracted release tarball
#   sudo ./install.sh /path/to/ventd-binary   # install a locally-built binary
#
# Environment overrides:
#   VENTD_VERSION  Pin to a specific release tag (e.g. v0.4.0). Default: latest.
#   VENTD_REPO     GitHub repo slug. Default: ventd/ventd.
#   VENTD_PREFIX   Install prefix for the binary. Default: /usr/local/bin.
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

TMPDIR_CLEANUP=""
cleanup() {
    if [[ -n "$TMPDIR_CLEANUP" && -d "$TMPDIR_CLEANUP" ]]; then
        rm -rf "$TMPDIR_CLEANUP"
    fi
}
trap cleanup EXIT

# ── Root check ───────────────────────────────────────────────────────────────

if [[ $EUID -ne 0 ]]; then
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

INIT_SYSTEM="$(detect_init)"

case "$INIT_SYSTEM" in
    systemd)
        SERVICE_SRC="$(find_unit ventd.service)" || {
            echo "error: ventd.service not found" >&2
            exit 1
        }
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

check_pwm_holders() {
    # Best-effort: fuser on sysfs is unreliable on some kernels but catches
    # the common case where a fan daemon not in the list above has a pwm
    # file open for write.
    if ! command -v fuser >/dev/null 2>&1; then
        return 0
    fi
    local holders
    holders="$(fuser /sys/class/hwmon/*/pwm[0-9]* 2>/dev/null || true)"
    if [[ -n "$holders" ]]; then
        echo "error: another process is holding /sys/class/hwmon/*/pwm<N> open:" >&2
        echo "" >&2
        fuser -v /sys/class/hwmon/*/pwm[0-9]* 2>&1 | grep -vE '^\s*$' >&2 || true
        echo "" >&2
        echo "  This is almost always another fan-control daemon. Identify it" >&2
        echo "  from the PID list above and stop it before installing ventd." >&2
        exit 1
    fi
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
            echo "  ventd's web UI binds 0.0.0.0:9999 by default. Stop the" >&2
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

echo "Checking for conflicting services..."
check_conflicting_daemon
check_pwm_holders
check_port_9999

# ── Install ──────────────────────────────────────────────────────────────────

echo "Installing ventd..."

install -d -m 755 "$VENTD_PREFIX"
install -m 755 "$BINARY" "$VENTD_PREFIX/ventd"
echo "  ✓ binary → $VENTD_PREFIX/ventd"

install -d -m 755 /etc/ventd
echo "  ✓ config dir → /etc/ventd/"

case "$INIT_SYSTEM" in

    systemd)
        install -m 644 "$SERVICE_SRC" /etc/systemd/system/ventd.service
        systemctl daemon-reload
        systemctl enable ventd.service
        systemctl start ventd.service
        echo "  ✓ systemd unit → /etc/systemd/system/ventd.service (enabled + started)"
        ;;

    openrc)
        install -m 755 "$OPENRC_SRC" /etc/init.d/ventd
        rc-update add ventd default
        rc-service ventd start
        echo "  ✓ OpenRC init script → /etc/init.d/ventd (enabled + started)"
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

        # Symlinking into the runit service directory enables AND starts the
        # service in one step — runsvdir picks up the symlink within seconds.
        if [ -d /var/service ]; then
            ln -sfn /etc/sv/ventd /var/service/ventd
            echo "  ✓ runit service → /etc/sv/ventd (linked in /var/service, auto-starts)"
        elif [ -d /etc/runit/runsvdir/default ]; then
            ln -sfn /etc/sv/ventd /etc/runit/runsvdir/default/ventd
            echo "  ✓ runit service → /etc/sv/ventd (linked in /etc/runit/runsvdir/default, auto-starts)"
        else
            echo "  ✓ runit service → /etc/sv/ventd"
            echo "  ! could not find service directory to link into; link manually:"
            echo "      ln -s /etc/sv/ventd /var/service/ventd"
        fi
        ;;

    unknown)
        echo "  ! no init system detected — service not registered"
        ;;
esac

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

if [[ "$INIT_SYSTEM" != "unknown" ]]; then
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

# ── Done ─────────────────────────────────────────────────────────────────────

MACHINE_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
WEB_URL="http://${MACHINE_IP:-<this-machine-ip>}:9999"

echo ""
echo "ventd installed. Open ${WEB_URL} to set up."

if [[ "$INIT_SYSTEM" == "unknown" ]]; then
    echo ""
    echo "(No supported init system detected. Start the daemon manually:"
    echo "   sudo $VENTD_PREFIX/ventd --config /etc/ventd/config.yaml)"
fi
