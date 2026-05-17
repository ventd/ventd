#!/bin/sh
# Post-remove hook for .deb / .rpm packages.
#
# Idempotency contract (PR#A4 of the v0.8.x setup-wizard rework):
#   - apt-get remove ventd / rpm -e ventd        → leave state alone
#   - apt-get purge ventd                        → wipe everything
#   - rpm full uninstall ($1 == 0)               → wipe everything
#
# The "wipe everything" path removes the entire ventd-managed state tree
# so a subsequent reinstall starts from a clean slate. Goal 3 of the
# v0.8.x wizard rework: no stale state ever carries forward.
#
# POSIX sh only.
#
# Argument semantics:
#
#   dpkg postrm arg:  remove | purge | upgrade | failed-upgrade |
#                     abort-install | abort-upgrade | disappear
#   rpm postun arg:   number of remaining installs (0 = full uninstall)

set -eu

ACTION=${1:-}

# Detect "full uninstall" across packager conventions. dpkg sets a
# string arg; rpm sets the count of remaining installs.
purge=0
case "$ACTION" in
    purge)
        purge=1
        ;;
    0)
        # rpm: zero remaining installs means full uninstall.
        purge=1
        ;;
esac

if [ "$purge" -ne 1 ]; then
    exit 0
fi

echo "ventd: purge requested; removing all ventd-managed state"

# 1. Orchestrator + calibration state (v0.8.x canonical location).
rm -rf /var/lib/ventd/setup/ 2>/dev/null || true

# 2. Legacy calibration path (v0.7.x and earlier) and its migration
# tombstone, in case the operator never started v0.8.x's daemon and
# the migrator never ran.
rm -f /etc/ventd/calibration.json /etc/ventd/calibration.json.moved-to-var-lib 2>/dev/null || true

# 3. Modprobe drop-ins ventd wrote (e.g. acpi_enforce_resources=lax,
# thinkpad_acpi options=fan_control=1). Pattern-match avoids touching
# drop-ins the operator wrote by hand.
for f in /etc/modprobe.d/ventd-*.conf /etc/modules-load.d/ventd-*.conf; do
    [ -e "$f" ] && rm -f "$f" 2>/dev/null || true
done

# 4. Generated TLS material + persistent applied-marker.
rm -f /var/lib/ventd/.setup-applied 2>/dev/null || true
rm -f /etc/ventd/tls.crt /etc/ventd/tls.key 2>/dev/null || true

# 5. KV store + diagnostics archives.
rm -rf /var/lib/ventd/state/ /var/lib/ventd/diag-bundles/ 2>/dev/null || true

# 6. Log directory.
rm -rf /var/log/ventd/ 2>/dev/null || true

# 7. Empty parent directories.
rmdir /etc/ventd 2>/dev/null || true
rmdir /var/lib/ventd 2>/dev/null || true

echo "ventd: purge complete; all ventd state removed"
exit 0
