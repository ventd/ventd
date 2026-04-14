#!/bin/sh
# Pre-remove hook for .deb / .rpm packages.
# Stops and disables the ventd service. Config files under /etc/ventd/ are preserved.

set -eu

if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet ventd.service 2>/dev/null; then
        systemctl stop ventd.service || true
    fi
    if systemctl is-enabled --quiet ventd.service 2>/dev/null; then
        systemctl disable ventd.service || true
    fi
fi

exit 0
