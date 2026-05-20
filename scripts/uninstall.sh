#!/usr/bin/env bash
# CRLF self-heal — matches install.sh.
[[ -f "$0" ]] && grep -lq $'\r' "$0" 2>/dev/null && sed -i 's/\r$//' "$0" && exec bash "$0" "$@"
# ventd uninstall script
#
# Usage:
#   sudo /usr/local/sbin/ventd-uninstall            # remove binary + service unit + state
#   sudo /usr/local/sbin/ventd-uninstall --keep-data  # binary + service only, leave /var/lib/ventd
#
# What this script does (in order):
#   1. systemctl disable --now ventd.service (if not already stopped by
#      the in-UI Reset to factory action).
#   2. rmmod whichever OOT driver ventd installed under
#      /lib/modules/<release>/extra/ and remove its DKMS registration
#      + /etc/modules-load.d entry.
#   3. Remove the systemd unit file at /etc/systemd/system/ventd.service
#      (and the deploy/ override drop-ins if present).
#   4. Remove the binary at $VENTD_PREFIX/ventd (default /usr/local/bin).
#   5. Remove /etc/ventd/ — config + auth + first-install timestamp.
#   6. Remove /var/lib/ventd/ — calibration, smart-mode shards, logs.
#      Skipped when --keep-data is passed.
#   7. Remove this script itself.
#
# Designed to be safe to re-run: every step uses idempotent commands
# (systemctl disable --now is a no-op on already-stopped units; rm -f
# never fails on missing paths).

set -euo pipefail

VENTD_PREFIX="${VENTD_PREFIX:-/usr/local/bin}"
KEEP_DATA=0
for arg in "$@"; do
    case "$arg" in
        --keep-data) KEEP_DATA=1 ;;
        -h|--help)
            sed -n '1,30p' "$0" | grep -E '^#( |$)' | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            echo "ventd-uninstall: unknown argument: $arg" >&2
            echo "Try: $0 --help" >&2
            exit 2
            ;;
    esac
done

if [[ $EUID -ne 0 ]]; then
    echo "ventd-uninstall: must run as root (try: sudo $0 $*)" >&2
    exit 1
fi

log() { printf '  %s\n' "$*"; }

echo "ventd uninstall starting"

# 1. Service.
if command -v systemctl >/dev/null 2>&1; then
    if systemctl list-unit-files 2>/dev/null | grep -q '^ventd\.service'; then
        log "disabling+stopping ventd.service"
        systemctl disable --now ventd.service 2>/dev/null || true
    fi
fi

# 2. OOT driver under /lib/modules/<release>/extra/. The in-UI factory
# reset path already did this via the daemon's CleanupOrphanInstall,
# but the script is idempotent for cases where the operator skipped
# the UI step (or never used it).
release="$(uname -r 2>/dev/null || true)"
extra_dir="/lib/modules/${release}/extra"
if [[ -n "$release" && -d "$extra_dir" ]]; then
    for ko in "$extra_dir"/*.ko; do
        [[ -e "$ko" ]] || continue
        mod="$(basename "$ko" .ko)"
        log "rmmod $mod (if loaded)"
        rmmod "$mod" 2>/dev/null || true
        if command -v dkms >/dev/null 2>&1; then
            if dkms status 2>/dev/null | grep -q "^$mod"; then
                log "dkms remove $mod"
                dkms remove --all "$mod" 2>/dev/null || true
            fi
        fi
        rm -f "$ko"
    done
fi
rm -f /etc/modules-load.d/ventd-*.conf 2>/dev/null || true

# 3. systemd unit file + drop-ins.
log "removing systemd unit + drop-ins"
rm -f /etc/systemd/system/ventd.service 2>/dev/null || true
rm -rf /etc/systemd/system/ventd.service.d 2>/dev/null || true
rm -f /usr/lib/systemd/system/ventd.service 2>/dev/null || true
rm -rf /usr/lib/systemd/system/ventd.service.d 2>/dev/null || true
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload 2>/dev/null || true
fi

# 4. Binary.
if [[ -x "$VENTD_PREFIX/ventd" ]]; then
    log "removing $VENTD_PREFIX/ventd"
    rm -f "$VENTD_PREFIX/ventd"
fi
# Common alternate install prefix.
if [[ -x "/usr/bin/ventd" ]]; then
    log "removing /usr/bin/ventd"
    rm -f "/usr/bin/ventd"
fi

# 5. Config.
if [[ -d /etc/ventd ]]; then
    log "removing /etc/ventd"
    rm -rf /etc/ventd
fi

# 6. Persistent state — opt-out via --keep-data.
if [[ $KEEP_DATA -eq 0 && -d /var/lib/ventd ]]; then
    log "removing /var/lib/ventd"
    rm -rf /var/lib/ventd
elif [[ $KEEP_DATA -eq 1 ]]; then
    log "leaving /var/lib/ventd in place (--keep-data)"
fi

# 7. Self.
self="$(readlink -f "$0" 2>/dev/null || true)"
if [[ -n "$self" && -f "$self" ]]; then
    log "removing $self"
    rm -f "$self"
fi

echo "ventd uninstall complete"
echo "Fans are under BIOS control. Reboot if anything looks off."
