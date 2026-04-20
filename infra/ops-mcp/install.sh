#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FRAGMENT="${SCRIPT_DIR}/logrotate.d/ops-mcp"
DEST=/etc/logrotate.d/ops-mcp

if [ -f "$DEST" ] && diff -q "$FRAGMENT" "$DEST" >/dev/null 2>&1; then
    echo "logrotate fragment already up-to-date: ${DEST}"
else
    install -m 0644 -o root -g root "$FRAGMENT" "$DEST"
    echo "installed logrotate fragment: ${DEST}"
fi
