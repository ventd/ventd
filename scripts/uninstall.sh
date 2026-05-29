#!/usr/bin/env bash
# CRLF self-heal — matches install.sh.
[[ -f "$0" ]] && grep -lq $'\r' "$0" 2>/dev/null && sed -i 's/\r$//' "$0" && exec bash "$0" "$@"
# ventd uninstall script
#
# Usage:
#   sudo /usr/local/sbin/ventd-uninstall                # remove binary + service unit + state + account
#   sudo /usr/local/sbin/ventd-uninstall --keep-data    # leave /var/lib/ventd, remove everything else
#   sudo /usr/local/sbin/ventd-uninstall --keep-user    # leave the ventd user/group, remove everything else
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
#   4. Remove every ventd helper binary in $VENTD_PREFIX and
#      $VENTD_SBIN_DIR — ventd, ventd-nvml-helper,
#      ventd-postreboot-verify.sh, ventd-recover, ventd-wait-hwmon.
#   5. Remove the udev rules ventd dropped + reload the rules.
#   6. Remove the AppArmor profiles ventd dropped — unloading via
#      apparmor_parser -R first so the kernel doesn't hold a reference
#      to the deleted file across the next service start.
#   7. Remove /etc/ventd/ — config + auth + first-install timestamp.
#   8. Remove /var/lib/ventd/ — calibration, smart-mode shards.
#      Skipped when --keep-data is passed.
#   9. Remove /var/log/ventd/ — install + SELinux-build logs.
#  10. Remove the ventd user and group. Skipped when --keep-user is passed.
#  11. Remove this script itself.

set -euo pipefail

VENTD_PREFIX="${VENTD_PREFIX:-/usr/local/bin}"
VENTD_SBIN_DIR="${VENTD_SBIN_DIR:-/usr/local/sbin}"
KEEP_DATA=0
KEEP_USER=0
for arg in "$@"; do
    case "$arg" in
        --keep-data) KEEP_DATA=1 ;;
        --keep-user) KEEP_USER=1 ;;
        -h|--help)
            sed -n '1,32p' "$0" | grep -E '^#( |$)' | sed 's/^# \{0,1\}//'
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

# 2. OOT driver(s) ventd installed under /lib/modules/<release>/extra/.
# The in-UI factory reset path already does this via the daemon's
# CleanupOrphanInstall (which is module-scoped); the script is the
# idempotent fallback for operators who skipped the UI step.
#
# CRITICAL: scope removal to exactly the module(s) ventd recorded in
# /etc/modules-load.d/ventd.conf — NEVER sweep every *.ko in extra/. That
# directory also holds unrelated out-of-tree / DKMS modules (nvidia, zfs,
# v4l2loopback, …) that ventd never installed; blindly rmmod-ing and
# dkms-removing them used to take out the GPU driver on uninstall.
release="$(uname -r 2>/dev/null || true)"
extra_dir="/lib/modules/${release}/extra"
load_conf="/etc/modules-load.d/ventd.conf"

# Read the authoritative list of modules ventd loaded: one bare module name
# per line, skipping comments/blanks (matches the Go reader in
# internal/hwmon, readModuleNames). Default IFS trims surrounding whitespace.
ventd_modules=()
if [[ -r "$load_conf" ]]; then
    while read -r mod _; do
        [[ -z "$mod" || "$mod" == \#* ]] && continue
        ventd_modules+=("$mod")
    done < "$load_conf"
fi

if [[ ${#ventd_modules[@]} -eq 0 ]]; then
    # No record of what ventd installed (legacy pre-record install, or the
    # conf was already removed). Do NOT guess by sweeping extra/*.ko — that
    # is how the old code removed unrelated modules. Leave them in place.
    log "no ventd module record in $load_conf — skipping kernel-module removal (refusing to sweep $extra_dir blindly)"
    log "  if ventd installed an out-of-tree driver, remove it manually:"
    log "    sudo modprobe -r <module> && sudo dkms remove --all <module>/<version>"
else
    for mod in "${ventd_modules[@]}"; do
        log "rmmod $mod (if loaded)"
        rmmod "$mod" 2>/dev/null || true
        if command -v dkms >/dev/null 2>&1 && dkms status 2>/dev/null | grep -q "^$mod"; then
            log "dkms remove $mod"
            dkms remove --all "$mod" 2>/dev/null || true
        fi
        # Remove only this module's object under extra/. Fedora/RHEL ship
        # compressed modules as `.ko.xz`, Arch as `.ko.zst`, Debian as plain
        # `.ko`; cover all three. Unrelated modules in extra/ are untouched.
        if [[ -n "$release" && -d "$extra_dir" ]]; then
            rm -f "$extra_dir/$mod.ko" "$extra_dir/$mod.ko.xz" "$extra_dir/$mod.ko.zst" 2>/dev/null || true
        fi
    done
fi
# Remove the module-load record itself (read above). The installer writes
# /etc/modules-load.d/ventd.conf (no dash); cover a legacy dashed variant too.
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
# into the bin/sbin prefixes. ventd-wait-hwmon and ventd-recover land
# in $VENTD_SBIN_DIR (default /usr/local/sbin), not $VENTD_PREFIX, so
# we have to check both trees on each helper.
for helper in ventd ventd-nvml-helper ventd-postreboot-verify.sh \
              ventd-recover ventd-wait-hwmon; do
    for dir in "$VENTD_PREFIX" /usr/bin "$VENTD_SBIN_DIR" /usr/sbin; do
        if [[ -e "$dir/$helper" ]]; then
            log "removing $dir/$helper"
            rm -f "$dir/$helper"
        fi
    done
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

# polkit rule for in-UI update (#1306). polkitd rescans rules.d/ on
# the next D-Bus call so no explicit reload is required.
if [[ -f /usr/share/polkit-1/rules.d/50-ventd-update.rules ]]; then
    log "removing polkit rule /usr/share/polkit-1/rules.d/50-ventd-update.rules"
    rm -f /usr/share/polkit-1/rules.d/50-ventd-update.rules
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

# 10. ventd system user + group. Belt-and-braces: only remove the user
# if no other process is running as it (the systemctl disable at step 1
# stops the only ventd-owned process the installer ships). The group is
# removed last so a botched user-removal leaves the group around as an
# obvious stranded artefact rather than silently losing both.
# Skipped under --keep-user when an operator wants the account to
# survive a reinstall (e.g. preserves usermod -aG memberships for nvml).
if [[ $KEEP_USER -eq 0 ]]; then
    if getent passwd ventd >/dev/null 2>&1; then
        if pgrep -u ventd >/dev/null 2>&1; then
            log "leaving ventd user (processes still running under it)"
        elif command -v userdel >/dev/null 2>&1; then
            log "removing ventd user"
            userdel ventd 2>/dev/null || true
        elif command -v deluser >/dev/null 2>&1; then
            # BusyBox deluser (Alpine, Void-musl).
            log "removing ventd user"
            deluser ventd 2>/dev/null || true
        fi
    fi
    if getent group ventd >/dev/null 2>&1; then
        if command -v groupdel >/dev/null 2>&1; then
            log "removing ventd group"
            groupdel ventd 2>/dev/null || true
        elif command -v delgroup >/dev/null 2>&1; then
            log "removing ventd group"
            delgroup ventd 2>/dev/null || true
        fi
    fi
else
    log "leaving ventd user/group in place (--keep-user)"
fi

# 11. Self.
self="$(readlink -f "$0" 2>/dev/null || true)"
if [[ -n "$self" && -f "$self" ]]; then
    log "removing $self"
    rm -f "$self"
fi

echo "ventd uninstall complete"
echo "Fans are under BIOS control. Reboot if anything looks off."
