#!/bin/sh
# Post-install hook for .deb / .rpm packages.
# Enables and starts the ventd systemd unit on systems where systemd is present.

set -eu

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
