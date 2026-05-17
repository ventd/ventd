#!/bin/sh
# Pre-install hook for .deb / .rpm packages.
#
# Idempotency contract (PR#A4 of the v0.8.x setup-wizard rework):
#   - Re-installing over a prior broken install MUST NOT inherit the
#     broken state. The preinst stops the running daemon (if any) and
#     warns about any stale state that postinst will need to clean up.
#   - Pre-install never DELETES state — Debian Policy forbids modifying
#     files outside the package scope without explicit operator action.
#     Stale-state cleanup is policy-clean only in postrm purge.
#
# POSIX sh only — Debian runs this under dash.
#
# Behaviour under each invocation mode:
#
#   dpkg first install                : $1 = "install"
#   dpkg upgrade                      : $1 = "upgrade <old-version>"
#   rpm install (any)                 : $1 = "1" (first) or "2" (upgrade)
#
# Stopping the running daemon during upgrade is the safe choice — the
# postinstall script restarts it, and an unstoppable old daemon would
# race the new binary's first reload.

set -eu

# Stop the daemon if it's running. systemctl returns 0 even when the
# unit isn't loaded, so we use `is-active --quiet` to gate the stop.
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet ventd.service 2>/dev/null; then
        echo "ventd: stopping running daemon before install/upgrade"
        systemctl stop ventd.service || true
    fi
fi

# Warn (but do NOT remove) on stale state from a prior broken install.
# The postinstall hook handles a fresh start; this is just informative.
stale_paths=
for path in \
    /var/lib/ventd/setup/state.json \
    /etc/ventd/calibration.json
do
    if [ -e "$path" ]; then
        stale_paths="$stale_paths $path"
    fi
done

if [ -n "$stale_paths" ]; then
    echo "ventd: pre-existing state detected;${stale_paths}"
    echo "ventd: the setup wizard will resume from these checkpoints if compatible,"
    echo "ventd: or invalidate them automatically on hardware/schema mismatch."
fi

exit 0
