#!/usr/bin/env bash
# ventd install script
# Usage:  sudo ./install.sh [/path/to/ventd-binary]
#
# What this script does:
#   1. Copies the binary to /usr/local/bin/ventd
#   2. Creates /etc/ventd/ for config and calibration data
#   3. Installs the service unit for your init system (systemd, OpenRC, or runit)
#   4. Enables the service (does NOT start it — see below)
#
# After installation, start the daemon and watch the log for your one-time
# setup token, then open http://<this-machine-ip>:9999 in a browser.

set -euo pipefail

BINARY="${1:-./ventd}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── Sanity checks ────────────────────────────────────────────────────────────

if [[ $EUID -ne 0 ]]; then
    echo "error: this script must be run as root (use sudo)" >&2
    exit 1
fi

if [[ ! -f "$BINARY" ]]; then
    echo "error: binary not found: $BINARY" >&2
    # On musl-based systems (Alpine, Void musl) CGO is unavailable; use the
    # nonvidia build tag to produce a fully static binary without go-nvml.
    if ls /lib/ld-musl-*.so.1 &>/dev/null 2>&1; then
        echo "  Build it with:  CGO_ENABLED=0 go build -tags nonvidia -o ventd ./cmd/ventd" >&2
    else
        echo "  Build it with:  go build -o ventd ./cmd/ventd" >&2
    fi
    exit 1
fi

# ── Init system detection ────────────────────────────────────────────────────

detect_init() {
    # Check for systemd first: presence of /run/systemd/system confirms it's
    # running (not just installed), so we don't false-positive on hybrid setups.
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

# Validate that the required service file(s) exist for the detected init system.
case "$INIT_SYSTEM" in
    systemd)
        SERVICE_SRC="$SCRIPT_DIR/ventd.service"
        if [[ ! -f "$SERVICE_SRC" ]]; then
            echo "error: ventd.service not found at $SERVICE_SRC" >&2
            exit 1
        fi
        ;;
    openrc)
        OPENRC_SRC="$SCRIPT_DIR/ventd.openrc"
        if [[ ! -f "$OPENRC_SRC" ]]; then
            echo "error: ventd.openrc not found at $OPENRC_SRC" >&2
            exit 1
        fi
        ;;
    runit)
        RUNIT_SRC="$SCRIPT_DIR/ventd.runit"
        if [[ ! -f "$RUNIT_SRC" ]]; then
            echo "error: ventd.runit not found at $RUNIT_SRC" >&2
            exit 1
        fi
        ;;
    unknown)
        echo "warning: no supported init system found (systemd, OpenRC, runit)."
        echo "  The binary will be installed but you will need to start ventd manually."
        ;;
esac

# ── Install ──────────────────────────────────────────────────────────────────

echo "Installing ventd..."

# Binary
install -m 755 "$BINARY" /usr/local/bin/ventd
echo "  ✓ binary → /usr/local/bin/ventd"

# Config directory
install -d -m 755 /etc/ventd
echo "  ✓ config dir → /etc/ventd/"

# ── Init-system-specific service installation ────────────────────────────────

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
        # Create the service directory and log directory.
        install -d -m 755 /etc/sv/ventd
        install -d -m 755 /etc/sv/ventd/log
        install -m 755 "$RUNIT_SRC" /etc/sv/ventd/run

        # Log run script: svlogd rotates logs under /var/log/ventd/.
        install -d -m 755 /var/log/ventd
        cat > /etc/sv/ventd/log/run <<'EOF'
#!/bin/sh
exec svlogd -tt /var/log/ventd
EOF
        chmod 755 /etc/sv/ventd/log/run

        # Enable by symlinking into the live service directory.
        # Void Linux uses /var/service; fall back to /etc/runit/runsvdir/default.
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
        echo "       sudo /usr/local/bin/ventd --config /etc/ventd/config.yaml"
        echo ""
        echo "  2. Watch its output for your one-time setup token."
        ;;
esac

echo ""
echo "  3. Open ${WEB_URL} in your browser to complete setup."
echo ""
echo "The daemon restarts automatically after crashes and on every boot."
