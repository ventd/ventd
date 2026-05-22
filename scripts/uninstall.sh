#!/usr/bin/env bash
# CRLF self-heal — matches install.sh.
[[ -f "$0" ]] && grep -lq $'\r' "$0" 2>/dev/null && sed -i 's/\r$//' "$0" && exec bash "$0" "$@"
# ventd uninstall script
#
# Usage:
#   sudo /usr/local/sbin/ventd-uninstall            # remove binary + service unit + state
#   sudo /usr/local/sbin/ventd-uninstall --keep-data  # binary + service only, leave /var/lib/ventd
#
# What this script does (in order — every step idempotent):
#   1. systemctl disable --now for every ventd-managed unit
#      (ventd.service, ventd-recover.service, ventd-postreboot-verify.service).
#   2. rmmod whichever OOT driver ventd installed under
#      /lib/modules/<release>/extra/, remove its DKMS registration,
#      and the /etc/modules-load.d entry the installer wrote.
#   3. Remove every ventd systemd unit + its drop-ins under
#      /etc/systemd/system AND /usr/lib/systemd/system (the installer
#      can land helpers in either tree depending on distro).
#   4. Remove every ventd helper binary in $VENTD_PREFIX —
#      ventd, ventd-nvml-helper, ventd-postreboot-verify.sh,
#      ventd-recover, ventd-wait-hwmon.
#   5. Remove the udev rules ventd dropped + reload the rules.
#   6. Remove the AppArmor profiles ventd dropped — unloading via
#      apparmor_parser -R first so the kernel doesn't hold a reference
#      to the deleted file across the next service start.
#   7. Remove /etc/ventd/ — config + auth + first-install timestamp.
#   8. Remove /var/lib/ventd/ — calibration, smart-mode shards.
#      Skipped when --keep-data is passed.
#   9. Remove /var/log/ventd/ — install + SELinux-build logs.
#  10. Remove this script itself.

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

# 1. Services. ventd-recover.service is enabled-on-install per
# deploy/ventd-recover.service, so leaving it behind would silently
# fire OnFailure logic at every boot against a vanished binary.
if command -v systemctl >/dev/null 2>&1; then
    for unit in ventd.service ventd-recover.service ventd-postreboot-verify.service; do
        if systemctl list-unit-files 2>/dev/null | grep -q "^${unit}"; then
            log "disabling+stopping ${unit}"
            systemctl disable --now "${unit}" 2>/dev/null || true
        fi
    done
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
# The installer writes /etc/modules-load.d/ventd.conf (no dash); the
# old glob `ventd-*.conf` never matched. Cover both shapes so a
# legacy install that wrote a dashed variant is also cleaned.
rm -f /etc/modules-load.d/ventd.conf /etc/modules-load.d/ventd-*.conf 2>/dev/null || true

# 3. systemd unit files + drop-ins for every unit the installer ships.
log "removing systemd units + drop-ins"
for unit_path in \
    /etc/systemd/system/ventd.service \
    /etc/systemd/system/ventd-recover.service \
    /etc/systemd/system/ventd-postreboot-verify.service \
    /usr/lib/systemd/system/ventd.service \
    /usr/lib/systemd/system/ventd-recover.service \
    /usr/lib/systemd/system/ventd-postreboot-verify.service; do
    rm -f "$unit_path" 2>/dev/null || true
done
for dropin_dir in \
    /etc/systemd/system/ventd.service.d \
    /etc/systemd/system/ventd-recover.service.d \
    /usr/lib/systemd/system/ventd.service.d \
    /usr/lib/systemd/system/ventd-recover.service.d; do
    rm -rf "$dropin_dir" 2>/dev/null || true
done
# The multi-user.target.wants/ symlink survives the unit-file removal
# in some systemd versions; clean it explicitly so daemon-reload
# doesn't trip on a dangling enabled symlink.
rm -f /etc/systemd/system/multi-user.target.wants/ventd-recover.service 2>/dev/null || true
if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload 2>/dev/null || true
fi

# 4. Binaries — the main daemon AND every helper the installer drops
# into the same prefix.
for helper in ventd ventd-nvml-helper ventd-postreboot-verify.sh \
              ventd-recover ventd-wait-hwmon; do
    if [[ -e "$VENTD_PREFIX/$helper" ]]; then
        log "removing $VENTD_PREFIX/$helper"
        rm -f "$VENTD_PREFIX/$helper"
    fi
    # Cover the alternate install prefix too.
    if [[ -e "/usr/bin/$helper" ]]; then
        log "removing /usr/bin/$helper"
        rm -f "/usr/bin/$helper"
    fi
done

# 5. udev rules. ventd's installer ships
# 90-ventd-hwmon.rules into both /etc/udev/rules.d (preferred) and
# /usr/lib/udev/rules.d (some distros). Reload after removal so the
# kernel drops the rule from its in-memory table.
udev_removed=0
for rule in /etc/udev/rules.d/90-ventd-hwmon.rules \
            /usr/lib/udev/rules.d/90-ventd-hwmon.rules; do
    if [[ -f "$rule" ]]; then
        log "removing udev rule $rule"
        rm -f "$rule"
        udev_removed=1
    fi
done
if [[ $udev_removed -eq 1 ]] && command -v udevadm >/dev/null 2>&1; then
    udevadm control --reload-rules 2>/dev/null || true
fi

# 6. AppArmor profiles. apparmor_parser -R must unload the profile
# from the kernel BEFORE the file is removed — otherwise the kernel
# holds a reference to the now-vanished path and the next service
# start refuses with "no such profile" rather than re-applying
# defaults.
if command -v apparmor_parser >/dev/null 2>&1; then
    for prof in /etc/apparmor.d/ventd /etc/apparmor.d/ventd-ipmi /etc/apparmor.d/ventd.compat; do
        if [[ -f "$prof" ]]; then
            log "unloading apparmor profile $prof"
            apparmor_parser -R "$prof" 2>/dev/null || true
        fi
    done
fi
for prof in /etc/apparmor.d/ventd /etc/apparmor.d/ventd-ipmi /etc/apparmor.d/ventd.compat; do
    if [[ -f "$prof" ]]; then
        log "removing apparmor profile file $prof"
        rm -f "$prof"
    fi
done

# 7. Config.
if [[ -d /etc/ventd ]]; then
    log "removing /etc/ventd"
    rm -rf /etc/ventd
fi

# 8. Persistent state — opt-out via --keep-data.
if [[ $KEEP_DATA -eq 0 && -d /var/lib/ventd ]]; then
    log "removing /var/lib/ventd"
    rm -rf /var/lib/ventd
elif [[ $KEEP_DATA -eq 1 ]]; then
    log "leaving /var/lib/ventd in place (--keep-data)"
fi

# 9. Logs.
if [[ -d /var/log/ventd ]]; then
    log "removing /var/log/ventd"
    rm -rf /var/log/ventd
fi

# 10. Self.
self="$(readlink -f "$0" 2>/dev/null || true)"
if [[ -n "$self" && -f "$self" ]]; then
    log "removing $self"
    rm -f "$self"
fi

echo "ventd uninstall complete"
echo "Fans are under BIOS control. Reboot if anything looks off."
