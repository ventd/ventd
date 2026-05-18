#!/bin/sh
# Pre-remove hook for .deb / .rpm packages.
# Stops and disables the ventd service on full uninstalls. Config files
# under /etc/ventd/ are always preserved.
#
# Upgrade detection (#1244): rpm runs the new package's %post BEFORE
# the old package's %preun on an upgrade — without skipping the stop
# here, every dnf/apt-get upgrade undoes the new postinstall's
# `systemctl restart`, leaving the operator with `inactive (dead)` and
# a manual `systemctl start ventd` step. Both package managers pass an
# argument to this script that distinguishes upgrade from uninstall;
# we honour both shapes:
#   rpm %preun: $1 = number of installed instances *after* this
#     removal. $1 >= 1 → upgrade (newer version remains).
#     $1 == 0 → full uninstall (last instance being removed).
#   dpkg prerm: $1 = verb ("remove" / "upgrade" / "deconfigure" /
#     "failed-upgrade"). "upgrade"/"failed-upgrade" → swap in progress.
#
# When invoked outside a package manager (manual `bash preremove.sh`
# or rpm transactions that don't pass an arg), $1 is unset; default
# to the safe full-uninstall path so the operator who runs the script
# directly gets the documented stop+disable behaviour.

set -eu

action="${1:-}"

# rpm path: integer $1.
case "$action" in
    [0-9]*)
        if [ "$action" -ge 1 ] 2>/dev/null; then
            # Another version of ventd remains after this transaction.
            # The new package's %post has already enabled+started its
            # own version; let that bind succeed by skipping our stop.
            exit 0
        fi
        ;;
esac

# dpkg path: upgrade verb.
case "$action" in
    upgrade|failed-upgrade)
        # apt/dpkg is replacing this version with another. The new
        # version's postinst will re-enable+start; skip our stop.
        exit 0
        ;;
esac

# Otherwise (action == "" or "remove" or "purge" or rpm $1 == "0"):
# proceed with the full-uninstall stop + disable.
if command -v systemctl >/dev/null 2>&1; then
    if systemctl is-active --quiet ventd.service 2>/dev/null; then
        systemctl stop ventd.service || true
    fi
    if systemctl is-enabled --quiet ventd.service 2>/dev/null; then
        systemctl disable ventd.service || true
    fi
fi

exit 0
