#!/bin/sh
# Post-install hook for .deb / .rpm packages.
#
# Creates the ventd system account, normalises ownership on /etc/ventd,
# reloads udev so the shipped rule applies without a reboot, and
# enables + starts the systemd unit.
#
# POSIX sh only — Debian runs this under dash.

set -eu

# Locate the shared account-creation helper. The package ships it at
# /usr/share/ventd/ (see .goreleaser.yml nfpms.contents).
for candidate in \
    /usr/share/ventd/_ventd_account.sh \
    /usr/local/share/ventd/_ventd_account.sh; do
    if [ -f "$candidate" ]; then
        # shellcheck source=scripts/_ventd_account.sh
        . "$candidate"
        break
    fi
done

if ! command -v ventd_create_account >/dev/null 2>&1; then
    echo "error: ventd account helper not found — package is incomplete" >&2
    exit 1
fi

ventd_create_account

# Record security-module status to /var/log/ventd/install.log so a
# silent confinement downgrade (see #202, #211) is still auditable
# after the .deb / .rpm postinstall output scrolls away. Best-effort.
# The .deb / .rpm flow leaves apparmor profile registration to the
# package manager's apparmor hook; we only log what ended up loaded.
log_security_outcome() {
    module="$1"; outcome="$2"; detail="$3"
    mkdir -p /var/log/ventd 2>/dev/null || return 0
    chmod 750 /var/log/ventd 2>/dev/null || true
    if getent group ventd >/dev/null 2>&1; then
        chown root:ventd /var/log/ventd 2>/dev/null || true
    fi
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date)"
    printf '%s %s=%s %s\n' "$ts" "$module" "$outcome" "$detail" \
        >> /var/log/ventd/install.log 2>/dev/null || true
    chmod 640 /var/log/ventd/install.log 2>/dev/null || true
    if getent group ventd >/dev/null 2>&1; then
        chown root:ventd /var/log/ventd/install.log 2>/dev/null || true
    fi
}

if [ -f /etc/apparmor.d/ventd ]; then
    if command -v aa-status >/dev/null 2>&1 \
       && aa-status --enabled 2>/dev/null \
       && aa-status 2>/dev/null | grep -qE '^[[:space:]]*ventd$'; then
        log_security_outcome apparmor loaded "profile=/etc/apparmor.d/ventd pkg=dpkg/rpm"
    else
        log_security_outcome apparmor refused "profile=/etc/apparmor.d/ventd pkg=dpkg/rpm hint=aa-status-did-not-list-profile"
    fi
else
    log_security_outcome apparmor skipped "reason=profile-not-shipped-by-pkg pkg=dpkg/rpm"
fi

if command -v semodule >/dev/null 2>&1 && semodule -l 2>/dev/null | grep -q '^ventd'; then
    log_security_outcome selinux loaded "module=ventd pkg=dpkg/rpm"
fi

# nfpms.contents writes config.example.yaml to /etc/ventd/ as root. The
# daemon will run as ventd:ventd and needs to read its own config dir,
# so normalise ownership and mode here.
if [ -d /etc/ventd ]; then
    chown -R ventd:ventd /etc/ventd
    chmod 0750 /etc/ventd
fi

# Apply the shipped udev rule (/lib/udev/rules.d/90-ventd-hwmon.rules)
# now instead of waiting for a reboot.
if command -v udevadm >/dev/null 2>&1; then
    udevadm control --reload >/dev/null 2>&1 || true
    udevadm trigger --subsystem-match=hwmon >/dev/null 2>&1 || true
fi

# Probe + persist hwmon kernel modules. Same reasoning as in
# scripts/install.sh: the daemon runs under ProtectKernelModules=yes
# and ProtectSystem=strict, so module loading and persistence have to
# happen here while we still hold root and live outside the sandbox.
# Best-effort — DiagnoseHwmon at daemon startup surfaces any miss with
# a remediation pointer.
for binpath in /usr/local/bin/ventd /usr/bin/ventd; do
    if [ -x "$binpath" ]; then
        "$binpath" --probe-modules >/dev/null 2>&1 || true
        break
    fi
done

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
    if ! systemctl is-enabled --quiet ventd.service 2>/dev/null; then
        systemctl enable ventd.service || true
    fi
    systemctl restart ventd.service || true

    # Post-reboot verifier (issue #111). The package already placed the
    # unit file and helper script on disk via nfpms.contents; opt into
    # enabling it by exporting VENTD_INSTALL_POSTREBOOT_VERIFY=1 before
    # running the package manager (dpkg / rpm / apt / dnf). Off by
    # default — reboot semantics are operator-controlled.
    if [ "${VENTD_INSTALL_POSTREBOOT_VERIFY:-0}" = "1" ]; then
        if [ -f /lib/systemd/system/ventd-postreboot-verify.service ] \
           || [ -f /usr/lib/systemd/system/ventd-postreboot-verify.service ]; then
            systemctl enable ventd-postreboot-verify.service || true
            echo "  ✓ post-reboot verifier enabled (fires on next boot)"
        fi
    fi

    echo ""
    echo "ventd installed. Open http://$(hostname -I | awk '{print $1}'):9999 to set up."
    echo "The one-time setup token is in: journalctl -u ventd --since '1 minute ago' | grep 'Setup token'"
    echo ""
fi

exit 0
