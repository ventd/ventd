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

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload || true
    if ! systemctl is-enabled --quiet ventd.service 2>/dev/null; then
        systemctl enable ventd.service || true
    fi
    systemctl restart ventd.service || true

    echo ""
    echo "ventd installed. Open http://$(hostname -I | awk '{print $1}'):9999 to set up."
    echo "The one-time setup token is in: journalctl -u ventd --since '1 minute ago' | grep 'Setup token'"
    echo ""
fi

exit 0
