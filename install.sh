#!/usr/bin/env bash
# ventd one-line installer. Usage:
#
#   curl -fsSL https://raw.githubusercontent.com/ventd/ventd/main/install.sh | sudo bash
#
# Or on a system where sudo can't read from a pipe:
#
#   curl -fsSL https://raw.githubusercontent.com/ventd/ventd/main/install.sh -o install.sh
#   sudo bash install.sh
#
# Detects arch (amd64 / arm64), picks the appropriate package format
# (.deb / .rpm / tarball fallback), verifies the artefact's SHA-256
# against the release's checksums.txt, installs ventd, enables and
# starts the systemd service, and prints the URL to open. After this
# command the user never touches the terminal again — the rest of
# setup is in the browser at https://<host>:9999.
#
# This is the install.sh referenced by .claude/rules/usability.md and
# tracked in issue #762.

set -euo pipefail

VENTD_VERSION="${VENTD_VERSION:-}"  # override to install a specific tag; default = latest
VENTD_REPO="${VENTD_REPO:-ventd/ventd}"
VENTD_LISTEN_PORT="${VENTD_LISTEN_PORT:-9999}"

# ── output helpers ────────────────────────────────────────────────────
log()  { printf '\033[1;34m→\033[0m %s\n' "$*"; }
ok()   { printf '\033[1;32m✓\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m✗\033[0m %s\n' "$*" >&2; exit 1; }

# ── pre-flight ────────────────────────────────────────────────────────
[ "$(uname -s)" = "Linux" ] || die "ventd is Linux-only; detected: $(uname -s)"

if [ "${EUID:-$(id -u)}" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
        warn "not running as root — re-invoking with sudo"
        exec sudo -E bash "$0" "$@"
    fi
    die "this script needs root privileges; install sudo or run as root"
fi

# ── arch detection ────────────────────────────────────────────────────
case "$(uname -m)" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "unsupported architecture: $(uname -m); ventd ships amd64 and arm64 binaries only" ;;
esac
log "architecture: linux/$arch"

# ── version resolution ────────────────────────────────────────────────
if [ -z "$VENTD_VERSION" ]; then
    log "resolving latest release from github.com/$VENTD_REPO"
    api="https://api.github.com/repos/$VENTD_REPO/releases/latest"
    VENTD_VERSION=$(curl -fsSL "$api" 2>/dev/null \
        | grep -E '"tag_name"' | head -1 | cut -d'"' -f4 || true)
    [ -n "$VENTD_VERSION" ] || die "could not resolve latest release; set VENTD_VERSION=v0.5.8 to override"
fi
log "version: $VENTD_VERSION"

version_num="${VENTD_VERSION#v}"
url_base="https://github.com/$VENTD_REPO/releases/download/$VENTD_VERSION"

# ── package-manager detection ─────────────────────────────────────────
if command -v dpkg >/dev/null && command -v apt-get >/dev/null; then
    pkg_format=deb
    pkg_install="dpkg -i"
    pkg_repair="apt-get install -fy --no-install-recommends"
elif command -v rpm >/dev/null && (command -v dnf >/dev/null || command -v yum >/dev/null); then
    pkg_format=rpm
    pkg_install="rpm -Uvh --replacepkgs"
    if command -v dnf >/dev/null; then pkg_repair="dnf install -y"; else pkg_repair="yum install -y"; fi
elif command -v pacman >/dev/null; then
    pkg_format=tar  # we don't ship .pkg.tar; tarball fallback for Arch
elif command -v zypper >/dev/null; then
    pkg_format=rpm
    pkg_install="zypper --non-interactive install --allow-unsigned-rpm"
    pkg_repair=""
elif command -v apk >/dev/null; then
    pkg_format=tar  # Alpine — tarball fallback (musl variant when available)
elif command -v xbps-install >/dev/null; then
    pkg_format=tar  # Void
else
    pkg_format=tar
fi
log "package format: $pkg_format"

# ── temp workspace ────────────────────────────────────────────────────
tmpbase="${TMPDIR:-/tmp}"
[ -w "$tmpbase" ] || tmpbase="/var/tmp"
tmp=$(mktemp -d "$tmpbase/ventd-install.XXXXXX")
trap 'rm -rf "$tmp"' EXIT

# ── select asset name ─────────────────────────────────────────────────
case "$pkg_format" in
    deb) asset="ventd_${version_num}_linux_${arch}.deb" ;;
    rpm) asset="ventd_${version_num}_linux_${arch}.rpm" ;;
    tar) asset="ventd_${version_num}_linux_${arch}.tar.gz" ;;
    *) die "internal error: unknown pkg_format=$pkg_format" ;;
esac

# ── download + verify ─────────────────────────────────────────────────
log "downloading $asset"
curl -fsSL "$url_base/$asset" -o "$tmp/$asset" \
    || die "failed to download $url_base/$asset"

log "fetching checksums.txt"
curl -fsSL "$url_base/checksums.txt" -o "$tmp/checksums.txt" \
    || die "failed to download checksums.txt"

log "verifying SHA-256"
expected=$(grep "[[:space:]]$asset\$" "$tmp/checksums.txt" | awk '{print $1}')
[ -n "$expected" ] || die "checksum entry for $asset not found in checksums.txt"
actual=$(sha256sum "$tmp/$asset" | awk '{print $1}')
[ "$expected" = "$actual" ] \
    || die "SHA-256 mismatch: expected $expected, got $actual; aborting (do not install corrupted artefact)"
ok "checksum verified"

# ── install ───────────────────────────────────────────────────────────
case "$pkg_format" in
    deb)
        log "installing via dpkg"
        if ! $pkg_install "$tmp/$asset" 2>&1 | tail -10; then
            warn "dpkg reported missing dependencies; running $pkg_repair"
            $pkg_repair || die "dependency repair failed; resolve manually with 'apt-get install -f'"
        fi
        ;;
    rpm)
        log "installing via rpm/${pkg_repair:-zypper}"
        $pkg_install "$tmp/$asset" || die "rpm install failed"
        ;;
    tar)
        log "installing tarball to /usr/local/bin"
        tar xzf "$tmp/$asset" -C "$tmp"
        binary="$tmp/ventd"
        [ -x "$binary" ] || binary=$(find "$tmp" -name ventd -type f -executable | head -1)
        [ -n "$binary" ] && [ -x "$binary" ] || die "no ventd binary found in tarball"
        install -m 0755 "$binary" /usr/local/bin/ventd
        warn "tarball install: systemd unit + ventd user/group + state dir not configured"
        warn "this fallback is NOT recommended for production; use deb or rpm distros where possible"
        ;;
esac

# ── enable + start ────────────────────────────────────────────────────
#
# When the start fails with status=231 (AppArmor confinement transition
# refused — kernel doesn't know about the profile label), the .deb /
# .rpm postinst forgot to call `apparmor_parser -r`. Issue #763. Detect
# this case and self-heal by loading any *ventd* profiles found under
# /etc/apparmor.d/, then retry the start once.
maybe_load_apparmor() {
    command -v apparmor_parser >/dev/null 2>&1 || return 1
    [ -d /etc/apparmor.d ] || return 1
    local found=0
    for prof in /etc/apparmor.d/ventd /etc/apparmor.d/ventd-ipmi; do
        [ -f "$prof" ] || continue
        log "loading missing AppArmor profile: $prof (workaround for #763)"
        apparmor_parser -r "$prof" 2>&1 | grep -v "Failed setting up policy cache" >&2 || true
        found=1
    done
    return $((!found))
}

if [ "$pkg_format" != "tar" ] && command -v systemctl >/dev/null; then
    log "enabling and starting ventd.service"
    systemctl enable ventd.service >/dev/null 2>&1 || true
    if systemctl restart ventd.service; then
        ok "ventd.service started"
    else
        # Inspect why it failed — status=231 = AppArmor (#763).
        exit_status=$(systemctl show ventd.service -p ExecMainStatus --value 2>/dev/null || echo "")
        if [ "$exit_status" = "231" ] && maybe_load_apparmor; then
            log "retrying ventd.service after AppArmor profile load"
            if systemctl restart ventd.service; then
                ok "ventd.service started (after AppArmor profile workaround)"
            else
                warn "ventd.service still failing after AppArmor workaround — check 'sudo journalctl -u ventd' for details"
            fi
        else
            warn "ventd.service failed to start — check 'sudo journalctl -u ventd' for details"
        fi
    fi
elif [ "$pkg_format" = "tar" ]; then
    warn "no systemd dispatch on tarball install — start manually with /usr/local/bin/ventd"
fi

# ── post-install hint ─────────────────────────────────────────────────
listen_addr=$(ip -4 -br addr 2>/dev/null \
    | awk '$2 == "UP" && $3 !~ /^127\./ {print $3}' \
    | cut -d/ -f1 | head -1)
listen_addr="${listen_addr:-127.0.0.1}"

cat <<EOF

$(ok "ventd $VENTD_VERSION installed.")

  Open https://${listen_addr}:${VENTD_LISTEN_PORT} in your browser to set up.

  (self-signed TLS — your browser will warn the first time; click through.)

EOF
