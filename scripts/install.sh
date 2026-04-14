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
#   5. Enables the service. It does NOT start it — the daemon prints a
#      one-time setup token on first run, and you want to see it.
#
# After installation, start the daemon, watch the log for the setup token,
# then open http://<this-machine-ip>:9999 in a browser.

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

    # Tag is like v0.4.0; archive name uses the version without the leading v.
    VER="${VENTD_VERSION#v}"
    ARCHIVE="ventd_${VER}_linux_${ARCH}.tar.gz"
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

    # Verify. sha256sum --ignore-missing so other archive lines don't error.
    echo "Verifying SHA-256..."
    ( cd "$TMPDIR_CLEANUP" && sha256sum --ignore-missing -c checksums.txt ) \
        || { echo "error: checksum mismatch for ${ARCHIVE}" >&2; exit 1; }

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
        echo "  ✓ systemd unit → /etc/systemd/system/ventd.service (enabled)"
        ;;

    openrc)
        install -m 755 "$OPENRC_SRC" /etc/init.d/ventd
        rc-update add ventd default
        echo "  ✓ OpenRC init script → /etc/init.d/ventd (added to default runlevel)"
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

        if [ -d /var/service ]; then
            ln -sfn /etc/sv/ventd /var/service/ventd
            echo "  ✓ runit service → /etc/sv/ventd (linked in /var/service)"
        elif [ -d /etc/runit/runsvdir/default ]; then
            ln -sfn /etc/sv/ventd /etc/runit/runsvdir/default/ventd
            echo "  ✓ runit service → /etc/sv/ventd (linked in /etc/runit/runsvdir/default)"
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

# ── Done ─────────────────────────────────────────────────────────────────────

MACHINE_IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
WEB_URL="http://${MACHINE_IP:-<this-machine-ip>}:9999"

echo ""
echo "Installation complete."
echo ""
echo "Next steps:"

case "$INIT_SYSTEM" in
    systemd)
        echo "  1. Start the daemon:"
        echo "       sudo systemctl start ventd"
        echo ""
        echo "  2. Get your one-time setup token:"
        echo "       sudo journalctl -u ventd -f"
        ;;
    openrc)
        echo "  1. Start the daemon:"
        echo "       sudo rc-service ventd start"
        echo ""
        echo "  2. Get your one-time setup token:"
        echo "       sudo tail -f /var/log/ventd.log"
        ;;
    runit)
        echo "  1. Start the daemon (symlink enables it immediately):"
        echo "       sudo sv start ventd"
        echo ""
        echo "  2. Get your one-time setup token:"
        echo "       sudo tail -f /var/log/ventd/current"
        ;;
    unknown)
        echo "  1. Start the daemon manually:"
        echo "       sudo $VENTD_PREFIX/ventd --config /etc/ventd/config.yaml"
        echo ""
        echo "  2. Watch its output for your one-time setup token."
        ;;
esac

echo ""
echo "  3. Open ${WEB_URL} in your browser to complete setup."
echo ""
echo "The daemon restarts automatically after crashes and on every boot."
