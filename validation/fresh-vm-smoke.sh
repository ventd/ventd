#!/usr/bin/env bash
# validation/fresh-vm-smoke.sh — fresh-VM install smoke harness for v0.2.0.
#
# Spins up a throwaway Incus system container per target distro, fetches a
# locally-built ventd binary and the release-shape asset tree over HTTP,
# runs scripts/install.sh inside the container, and asserts the daemon
# starts clean. Writes a per-target markdown report next to this script.
#
# Usage:
#   validation/fresh-vm-smoke.sh                      # run all targets
#   validation/fresh-vm-smoke.sh all                  # ditto
#   validation/fresh-vm-smoke.sh ubuntu-24.04         # one target
#   validation/fresh-vm-smoke.sh ubuntu-24.04 arch    # several targets
#
# Flags:
#   --refresh-images   Delete the locally cached image for each selected
#                      target before launch so the subsequent `incus launch`
#                      re-pulls from the images: remote. Opt-in because the
#                      re-pull adds ~20 s per target; normal runs reuse the
#                      warm cache. Use this when the local squashfs cache
#                      is suspect (see validation/README.md "Recovery:
#                      corrupted cached image").
#
# Environment overrides:
#   VENTD_SMOKE_BRIDGE   Incus bridge name (default: incusbr0).
#   VENTD_SMOKE_PORT     HTTP port on the host (default: 8089).
#   VENTD_SMOKE_IMAGES_REMOTE
#                        Image remote prefix (default: images).
#                        Override to "ubuntu" etc. if "images:" isn't
#                        configured; ubuntu: only covers ubuntu variants.
#   VENTD_SMOKE_TIMEOUT_BOOT
#                        Seconds to wait for container network (default: 60).
#   VENTD_SMOKE_TIMEOUT_PING
#                        Seconds to wait for daemon /api/ping (default: 60).
#   VENTD_SMOKE_KEEP     Set to 1 to skip instance cleanup on completion;
#                        useful while debugging a failing target. Harness
#                        still tears down on Ctrl-C / error exit.
#
# Requirements (preflight-checked):
#   - incus CLI on PATH with a working daemon (run once: `sudo incus admin init --auto`).
#   - The "images:" remote (default). Add manually if missing:
#       incus remote add images https://images.linuxcontainers.org --protocol simplestreams
#   - go, python3, tar, curl on the host.
#
# See validation/README.md for distro-specific prerequisites.

set -euo pipefail

# ── Resolve paths ─────────────────────────────────────────────────────────

HARNESS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$HARNESS_DIR/.." && pwd)"
BUILD_DIR="$HARNESS_DIR/.build"
TARBALL="$BUILD_DIR/ventd-smoke.tar.gz"
LOG_DIR="$BUILD_DIR/logs"

BRIDGE="${VENTD_SMOKE_BRIDGE:-incusbr0}"
HTTP_PORT="${VENTD_SMOKE_PORT:-8089}"
IMAGES_REMOTE="${VENTD_SMOKE_IMAGES_REMOTE:-images}"
TIMEOUT_BOOT="${VENTD_SMOKE_TIMEOUT_BOOT:-60}"
TIMEOUT_PING="${VENTD_SMOKE_TIMEOUT_PING:-60}"
KEEP="${VENTD_SMOKE_KEEP:-0}"
REFRESH_IMAGES=0

RUN_ID="$$-$(date -u +%Y%m%dT%H%M%SZ)"
HTTP_PID=""
declare -a ACTIVE_INSTANCES=()

# ── Target matrix ─────────────────────────────────────────────────────────
#
# Keep image aliases on images.linuxcontainers.org. Each entry is
# distro-key → incus image alias. Add a target by appending a line and
# listing the key in ALL_TARGETS.

declare -A IMAGES=(
    [ubuntu-24.04]="$IMAGES_REMOTE:ubuntu/24.04"
    [debian-12]="$IMAGES_REMOTE:debian/12"
    [fedora-42]="$IMAGES_REMOTE:fedora/42"
    [arch]="$IMAGES_REMOTE:archlinux"
    [opensuse-tumbleweed]="$IMAGES_REMOTE:opensuse/tumbleweed"
    [alpine]="$IMAGES_REMOTE:alpine/3.20"
)

ALL_TARGETS=(ubuntu-24.04 debian-12 fedora-42 arch opensuse-tumbleweed alpine)

# ── Logging helpers ───────────────────────────────────────────────────────

log()   { printf '%s [%s] %s\n' "$(date -u +%H:%M:%S)" "$1" "${*:2}"; }
info()  { log INFO "$*"; }
warn()  { log WARN "$*" >&2; }
err()   { log ERR  "$*" >&2; }

# ── Cleanup trap ──────────────────────────────────────────────────────────

cleanup() {
    local ec=$?
    trap - EXIT INT TERM

    if [[ -n "$HTTP_PID" ]] && kill -0 "$HTTP_PID" 2>/dev/null; then
        info "stopping http.server (pid $HTTP_PID)"
        kill "$HTTP_PID" 2>/dev/null || true
        wait "$HTTP_PID" 2>/dev/null || true
    fi

    if [[ "$KEEP" != "1" ]]; then
        for inst in "${ACTIVE_INSTANCES[@]}"; do
            if incus info "$inst" &>/dev/null; then
                info "deleting instance $inst"
                incus delete --force "$inst" &>/dev/null || true
            fi
        done
    else
        if (( ${#ACTIVE_INSTANCES[@]} > 0 )); then
            warn "VENTD_SMOKE_KEEP=1 — left instances running: ${ACTIVE_INSTANCES[*]}"
        fi
    fi

    exit "$ec"
}
trap cleanup EXIT INT TERM

# ── Preflight ─────────────────────────────────────────────────────────────

preflight() {
    local missing=()
    for cmd in incus go python3 tar curl awk sed; do
        command -v "$cmd" >/dev/null 2>&1 || missing+=("$cmd")
    done
    if (( ${#missing[@]} > 0 )); then
        err "missing required commands: ${missing[*]}"
        err "see validation/README.md for setup prerequisites"
        exit 2
    fi

    if ! incus info >/dev/null 2>&1; then
        err "incus daemon not reachable. Run once: sudo incus admin init --auto"
        err "and ensure your user is in the 'incus-admin' group, then re-login."
        exit 2
    fi

    if ! ip -4 -o addr show dev "$BRIDGE" >/dev/null 2>&1; then
        err "bridge $BRIDGE not present. Override with VENTD_SMOKE_BRIDGE=<name>."
        exit 2
    fi

    HOST_IP="$(ip -4 -o addr show dev "$BRIDGE" | awk '{print $4}' | cut -d/ -f1 | head -1)"
    if [[ -z "$HOST_IP" ]]; then
        err "no IPv4 address on bridge $BRIDGE"
        exit 2
    fi

    if ss -lnt "sport = :$HTTP_PORT" 2>/dev/null | grep -q "LISTEN"; then
        err "port $HTTP_PORT already bound. Override with VENTD_SMOKE_PORT."
        exit 2
    fi
}

# ── Build stage: binary + release-shape tarball ───────────────────────────

build_tarball() {
    info "building ventd binary ($PROJECT_ROOT)"
    mkdir -p "$BUILD_DIR" "$LOG_DIR"

    local staging="$BUILD_DIR/staging"
    rm -rf "$staging"
    mkdir -p "$staging/scripts" "$staging/deploy"

    ( cd "$PROJECT_ROOT" && go build -trimpath -o "$staging/ventd" ./cmd/ventd )
    chmod 0755 "$staging/ventd"

    cp "$PROJECT_ROOT/scripts/install.sh"        "$staging/scripts/"
    cp "$PROJECT_ROOT/scripts/postinstall.sh"    "$staging/scripts/"
    cp "$PROJECT_ROOT/scripts/preremove.sh"      "$staging/scripts/"
    cp "$PROJECT_ROOT/scripts/_ventd_account.sh" "$staging/scripts/"
    cp "$PROJECT_ROOT/scripts/ventd.openrc"      "$staging/scripts/"
    cp "$PROJECT_ROOT/scripts/ventd.runit"       "$staging/scripts/"
    cp "$PROJECT_ROOT/deploy/ventd.service"         "$staging/deploy/"
    cp "$PROJECT_ROOT/deploy/ventd-recover.service" "$staging/deploy/"
    cp "$PROJECT_ROOT/config.example.yaml"       "$staging/"

    chmod 0755 "$staging/scripts/install.sh" "$staging/scripts/postinstall.sh" \
               "$staging/scripts/preremove.sh" "$staging/scripts/_ventd_account.sh" \
               "$staging/scripts/ventd.openrc" "$staging/scripts/ventd.runit"

    ( cd "$staging" && tar -czf "$TARBALL" \
        ventd scripts/ deploy/ config.example.yaml )

    info "tarball ready: $TARBALL ($(stat -c%s "$TARBALL") bytes)"
}

# ── HTTP server ───────────────────────────────────────────────────────────

start_http() {
    info "serving $BUILD_DIR on http://$HOST_IP:$HTTP_PORT/"
    ( cd "$BUILD_DIR" && python3 -m http.server "$HTTP_PORT" --bind "$HOST_IP" ) \
        >"$LOG_DIR/http.log" 2>&1 &
    HTTP_PID=$!
    # Give python a moment to bind; fail fast if it doesn't.
    local tries=0
    while (( tries < 20 )); do
        if curl -sf -o /dev/null "http://$HOST_IP:$HTTP_PORT/ventd-smoke.tar.gz"; then
            return 0
        fi
        sleep 0.2
        tries=$((tries + 1))
    done
    err "http.server failed to serve tarball within 4s. Log: $LOG_DIR/http.log"
    exit 3
}

# ── Container helpers ─────────────────────────────────────────────────────

refresh_image() {
    # When --refresh-images is set, drop the local cache entry for $image
    # before the launch that follows. Silent no-op if the image isn't
    # cached locally (nothing to refresh), or if incus image info can't
    # reach the remote — launch will surface that error in its own voice.
    local image="$1"
    local fp
    fp="$(incus image info "$image" 2>/dev/null | awk '/^Fingerprint:/ {print $2; exit}')"
    if [[ -z "$fp" ]]; then
        return 0
    fi
    local prefix="${fp:0:12}"
    if incus image list --format=csv -c f 2>/dev/null | grep -qx "$prefix"; then
        info "refresh-images: deleting cached $prefix for $image"
        incus image delete "$prefix" >/dev/null 2>&1 || true
    fi
}

launch_container() {
    local inst="$1" image="$2"
    if (( REFRESH_IMAGES == 1 )); then
        refresh_image "$image"
    fi
    info "launching $inst from $image"
    if ! incus launch "$image" "$inst" >/dev/null; then
        err "incus launch failed for $image"
        return 1
    fi
    ACTIVE_INSTANCES+=("$inst")

    local deadline=$(( $(date +%s) + TIMEOUT_BOOT ))
    while (( $(date +%s) < deadline )); do
        if incus exec "$inst" -- sh -c "command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || true" 2>/dev/null; then
            # Network up if we can reach the host.
            if incus exec "$inst" -- sh -c "ping -c1 -W2 $HOST_IP >/dev/null 2>&1"; then
                return 0
            fi
        fi
        sleep 1
    done
    err "container $inst did not come up in ${TIMEOUT_BOOT}s"
    return 1
}

# Run a shell snippet inside the container, tagging output lines.
# Returns the snippet's exit code.
cexec() {
    local inst="$1"; shift
    incus exec "$inst" -- sh -c "$*"
}

# Install the bootstrap tooling we need inside the container (curl, tar).
# Distros where these aren't in the base image: alpine, sometimes debian.
bootstrap_tools() {
    local inst="$1" target="$2"
    case "$target" in
        alpine)
            cexec "$inst" "apk add --no-cache curl tar bash >/dev/null" ;;
        debian-12)
            cexec "$inst" "command -v curl >/dev/null || (apt-get update -qq && apt-get install -y -qq curl tar ca-certificates)" ;;
        ubuntu-24.04)
            cexec "$inst" "command -v curl >/dev/null || (apt-get update -qq && apt-get install -y -qq curl tar ca-certificates)" ;;
        fedora-42)
            cexec "$inst" "command -v curl >/dev/null || dnf -y -q install curl tar" ;;
        arch)
            cexec "$inst" "command -v curl >/dev/null || pacman -Sy --noconfirm --quiet curl tar >/dev/null" ;;
        opensuse-tumbleweed)
            cexec "$inst" "command -v curl >/dev/null || zypper -q -n install curl tar" ;;
    esac
}

# ── Assertions ────────────────────────────────────────────────────────────
#
# Each assertion runs inside the container and prints:
#   PASS|FAIL <tab> <assertion-id> <tab> <short-message>
# followed on FAIL by a fenced "log tail" block.
#
# The top-level run_target() collects these into the per-target report.

assertion() {
    local id="$1" verdict="$2" msg="$3"
    printf '%s\t%s\t%s\n' "$verdict" "$id" "$msg"
}

assert_install_exit() {
    local inst="$1"
    # Install log was captured; verdict is exit code from the install step.
    if [[ "$2" == "0" ]]; then
        assertion A1 PASS "install.sh exit 0"
    else
        assertion A1 FAIL "install.sh exit $2"
        echo '```'
        cexec "$inst" "tail -n 60 /tmp/install.log 2>/dev/null || echo '(no install log)'"
        echo '```'
    fi
}

assert_service_active() {
    local inst="$1" init="$2"
    case "$init" in
        systemd)
            if cexec "$inst" "systemctl is-active --quiet ventd"; then
                assertion A2 PASS "systemctl is-active ventd"
            else
                assertion A2 FAIL "systemctl is-active ventd"
                echo '```'
                cexec "$inst" "systemctl status ventd --no-pager -l | tail -n 40"
                echo '```'
            fi
            ;;
        openrc)
            if cexec "$inst" "rc-service ventd status | grep -qiE 'started|running'"; then
                assertion A2 PASS "rc-service ventd started"
            else
                assertion A2 FAIL "rc-service ventd not running"
                echo '```'
                cexec "$inst" "rc-service ventd status 2>&1 | tail -n 20"
                echo '```'
            fi
            ;;
        *)
            assertion A2 SKIP "unknown init system: $init"
            ;;
    esac
}

assert_api_ping() {
    local inst="$1"
    # ventd refuses plaintext HTTP on non-loopback listens — see
    # Web.RequireTransportSecurity() in internal/config/config.go. On
    # first boot it auto-generates a self-signed cert under the config
    # dir (cmd/ventd/main.go:132-162) and serves HTTPS on 0.0.0.0:9999.
    # That's the documented posture, so this assertion hits HTTPS with
    # -k (the cert is unconditionally self-signed; no trust chain to
    # verify against) and -m 3 so a non-listening port fails a probe
    # in 3s instead of hanging for the default connect timeout.
    local deadline=$(( $(date +%s) + TIMEOUT_PING ))
    while (( $(date +%s) < deadline )); do
        if cexec "$inst" "curl -sfm 3 -k -o /dev/null https://127.0.0.1:9999/api/ping"; then
            assertion A3 PASS "curl -k https://127.0.0.1:9999/api/ping → 200"
            return 0
        fi
        sleep 1
    done
    assertion A3 FAIL "api/ping never returned 200 within ${TIMEOUT_PING}s"
    echo '```'
    cexec "$inst" "curl -kv -m 3 https://127.0.0.1:9999/api/ping 2>&1 | tail -n 20"
    echo '```'
}

# A4 — first-boot wizard mode. GET /api/auth/state is unauthenticated
# (handleAuthState in internal/web/server.go) and returns
# {"first_boot":true|false}. true means no password_hash is set, which
# is exactly the install-smoke invariant: the install script leaves the
# operator at the wizard, not at a dashboard. An authoritative JSON
# probe beats HTML scraping the login page, which always contains the
# wizard HTML (a CSS class toggles it).
assert_wizard_mode() {
    local inst="$1"
    local body
    body=$(cexec "$inst" "curl -sfm 3 -k https://127.0.0.1:9999/api/auth/state" 2>/dev/null || echo "")
    if [[ "$body" == *'"first_boot":true'* ]]; then
        assertion A4 PASS "/api/auth/state → first_boot:true"
    else
        assertion A4 FAIL "daemon not in first-boot mode. Body: ${body:-"(empty)"}"
        echo '```'
        cexec "$inst" "curl -kv -m 3 https://127.0.0.1:9999/api/auth/state 2>&1 | tail -n 20"
        echo '```'
    fi
}

assert_setup_token() {
    local inst="$1" init="$2"
    case "$init" in
        systemd)
            if cexec "$inst" "journalctl -u ventd --since '5 min ago' --no-pager | grep -iE 'setup.token|one-time' | head -n 1 | grep -q ."; then
                assertion A5 PASS "setup token present in journal"
            else
                assertion A5 FAIL "no 'setup token' line in journalctl -u ventd (last 5 min)"
                echo '```'
                cexec "$inst" "journalctl -u ventd --no-pager | tail -n 20 2>&1"
                echo '```'
            fi
            ;;
        openrc)
            if cexec "$inst" "grep -iE 'setup.token|one-time' /var/log/ventd/current /var/log/messages /var/log/syslog 2>/dev/null | head -n 1 | grep -q ."; then
                assertion A5 PASS "setup token present in log"
            else
                assertion A5 FAIL "no 'setup token' line in /var/log/ventd or syslog"
                echo '```'
                cexec "$inst" "tail -n 40 /var/log/ventd/current 2>/dev/null || tail -n 40 /var/log/messages 2>/dev/null || echo '(no log found)'"
                echo '```'
            fi
            ;;
    esac
}

# A6 — /etc/ventd if present is ventd:ventd. Replaces the old config.yaml
# seed-and-restart regression check for PR #38: install.sh creates the
# directory (at 0750 ventd:ventd) but does NOT create config.yaml —
# the setup wizard does, and driving the wizard needs real hwmon.
# Seeding config.example.yaml to make config.yaml exist blew up on
# Incus where /sys is inherited from the host and the "nct6687 matches
# multiple hwmon devices" guard (PR #42) fires. The invariant we
# actually care about is the directory's ownership: if install.sh
# leaves /etc/ventd owned by root (the PR #38 bug), the daemon
# (User=ventd) can't write config or cert files into it.
assert_config_dir_owner() {
    local inst="$1"
    if ! cexec "$inst" "test -d /etc/ventd" 2>/dev/null; then
        assertion A6 SKIP "/etc/ventd not present (install did not create it)"
        return
    fi
    local stat_line mode=""
    stat_line=$(cexec "$inst" "stat -c '%U %G %a' /etc/ventd" 2>/dev/null || echo "")
    case "$stat_line" in
        "ventd ventd "*)
            mode="${stat_line##* }"
            assertion A6 PASS "/etc/ventd owner=ventd:ventd mode=0${mode}"
            ;;
        *)
            assertion A6 FAIL "/etc/ventd ownership wrong: $stat_line"
            echo '```'
            cexec "$inst" "ls -ld /etc/ventd && ls -la /etc/ventd/"
            echo '```'
            ;;
    esac
}

# A7 — no fatal / hwmon-refusal lines in journal over the last 2 minutes.
# Catches regressions where the daemon crashes during first-boot
# initialisation (cert gen, hwmon scan, account setup). The example
# phrase we're guarding against is the PR #42 message the seed flow
# used to produce:
#   level=ERROR msg="ventd: fatal" err="load config: resolve hwmon ..."
#   systemd[1]: ventd.service: Failed with result 'exit-code'.
# "WARN" lines from the hwmon watcher on a hardware-less container
# are expected and not matched.
assert_no_fatal() {
    local inst="$1" init="$2"
    local query
    case "$init" in
        systemd)
            query="journalctl -u ventd --since '2 min ago' --no-pager"
            ;;
        openrc)
            query="tail -n 200 /var/log/ventd/current 2>/dev/null || tail -n 200 /var/log/messages 2>/dev/null || true"
            ;;
        *)
            assertion A7 SKIP "unknown init: $init"; return ;;
    esac
    local hits
    hits=$(cexec "$inst" "$query | grep -iE 'level=error.*fatal|ventd: fatal|Failed with result|matches multiple hwmon' | head -n 5" 2>/dev/null || echo "")
    if [[ -z "$hits" ]]; then
        assertion A7 PASS "no fatal lines in last 2 min"
    else
        assertion A7 FAIL "fatal lines found in last 2 min"
        echo '```'
        printf '%s\n' "$hits"
        echo '```'
    fi
}

# A8 — hardened-unit regression gate. The ventd.service unit sets
# User=ventd; the binary must actually end up running as that user
# (not root). This is the invariant the old A6 was meant to check —
# the previous harness design masked it whenever the A5 seed broke
# the daemon. Now that A5 doesn't touch the daemon, pidof finds a
# running process and ps reports its user reliably.
assert_run_user() {
    local inst="$1"
    local user
    user=$(cexec "$inst" "ps -o user= -p \$(pidof ventd) 2>/dev/null | tr -d ' '" 2>/dev/null || echo "")
    if [[ "$user" == "ventd" ]]; then
        assertion A8 PASS "ventd process owned by user 'ventd'"
    else
        assertion A8 FAIL "ventd process user: '$user' (expected 'ventd')"
        echo '```'
        cexec "$inst" "ps -eo pid,user,comm | grep -E 'ventd|PID' | head -n 10"
        echo '```'
    fi
}

assert_uninstall() {
    local inst="$1" init="$2"
    local cmds
    case "$init" in
        systemd)
            cmds='
set -e
systemctl stop ventd
rm -rf /etc/ventd /usr/local/bin/ventd /etc/systemd/system/ventd.service /etc/systemd/system/ventd-recover.service
systemctl daemon-reload
'
            ;;
        openrc)
            cmds='
set -e
rc-service ventd stop || true
rc-update del ventd default 2>/dev/null || true
rm -rf /etc/ventd /usr/local/bin/ventd /etc/init.d/ventd
'
            ;;
        *)
            assertion A9 SKIP "unknown init: $init"; return ;;
    esac

    if ! cexec "$inst" "$cmds" >/tmp/uninstall.log 2>&1; then
        assertion A9 FAIL "uninstall commands returned non-zero"
        echo '```'
        cat /tmp/uninstall.log
        echo '```'
        rm -f /tmp/uninstall.log
        return
    fi
    rm -f /tmp/uninstall.log

    local orphan_check='
bad=0
for p in /usr/local/bin/ventd /etc/ventd /etc/systemd/system/ventd.service /etc/init.d/ventd; do
    if [ -e "$p" ]; then echo "orphan: $p"; bad=1; fi
done
if pidof ventd >/dev/null 2>&1; then echo "orphan: ventd process still running"; bad=1; fi
# ventd user/group may legitimately linger (userdel not called) — allow
exit $bad
'
    if cexec "$inst" "$orphan_check"; then
        assertion A9 PASS "uninstall leaves no on-disk orphans"
    else
        assertion A9 FAIL "uninstall left orphans"
        echo '```'
        cexec "$inst" "$orphan_check 2>&1 || true"
        echo '```'
    fi
}

# ── Per-target run ────────────────────────────────────────────────────────

run_target() {
    local target="$1"
    local image="${IMAGES[$target]:-}"
    if [[ -z "$image" ]]; then
        err "unknown target: $target. Known: ${!IMAGES[*]}"
        return 1
    fi

    # Incus instance names: [a-z0-9-], no trailing dash, max 63 chars.
    # RUN_ID carries ISO-8601 uppercase (T, Z); lowercase first, then sanitise.
    local inst="ventd-smoke-${target//./-}-${RUN_ID,,}"
    inst="${inst//[^a-z0-9-]/-}"
    inst="${inst:0:63}"
    inst="${inst%"${inst##*[!-]}"}"   # strip trailing dashes

    local date_tag
    date_tag=$(date -u +%Y%m%d-%H%M)
    local report="$HARNESS_DIR/fresh-vm-smoke-${target}-${date_tag}.md"

    info "=== target: $target (instance: $inst) ==="

    # Open report with a transient header we'll overwrite with verdict.
    {
        echo "# fresh-VM install smoke — $target"
        echo
        echo "- Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
        echo "- Image: \`$image\`"
        echo "- Instance: \`$inst\`"
        echo "- Binary: \`$(cd "$PROJECT_ROOT" && git describe --always --dirty)\`"
        echo "- Host: \`$(uname -srm)\` on bridge \`$BRIDGE\` (\`$HOST_IP\`)"
        echo
        echo "## Assertions"
        echo
    } > "$report"

    if ! launch_container "$inst" "$image"; then
        echo "FAIL: container never came up." >> "$report"
        echo >> "$report"
        echo "## Overall: FAIL" >> "$report"
        return 1
    fi

    bootstrap_tools "$inst" "$target" || {
        echo "FAIL: bootstrap tooling install failed." >> "$report"
        echo >> "$report"
        echo "## Overall: FAIL" >> "$report"
        return 1
    }

    local init
    init=$(cexec "$inst" "[ -d /run/systemd/system ] && echo systemd || (command -v rc-service >/dev/null 2>&1 && echo openrc || echo unknown)" 2>/dev/null || echo unknown)
    info "init system in $inst: $init"

    # Fetch tarball inside the container and extract to /opt/ventd-smoke.
    # Then run scripts/install.sh from the extracted tree. This exercises
    # the "release tarball" path in install.sh (BINARY=../ventd resolution).
    # The install output is captured inside the container at /tmp/install.log
    # so assert_install_exit can tail it on FAIL. A host-side mirror is kept
    # under $LOG_DIR for post-mortem even after the container is deleted.
    local fetch_install='
set -e
mkdir -p /opt/ventd-smoke
cd /opt/ventd-smoke
curl -sSf -o ventd-smoke.tar.gz http://'"$HOST_IP"':'"$HTTP_PORT"'/ventd-smoke.tar.gz
tar -xzf ventd-smoke.tar.gz
rm -f ventd-smoke.tar.gz
bash scripts/install.sh >/tmp/install.log 2>&1
'
    local install_rc=0
    cexec "$inst" "$fetch_install" >"$LOG_DIR/install-$target.log" 2>&1 || install_rc=$?

    # Run assertions, capturing stdout line-by-line into the report.
    # Order traces the install flow: install exits → service comes up →
    # API reachable → daemon is in first-boot wizard mode → setup token
    # surfaces in the journal → config dir ownership is correct → no
    # fatal lines got logged → process identity is the hardened user →
    # clean uninstall.
    {
        assert_install_exit     "$inst" "$install_rc"
        assert_service_active   "$inst" "$init"
        assert_api_ping         "$inst"
        assert_wizard_mode      "$inst"
        assert_setup_token      "$inst" "$init"
        assert_config_dir_owner "$inst"
        assert_no_fatal         "$inst" "$init"
        assert_run_user         "$inst"
        assert_uninstall        "$inst" "$init"
    } >> "$report"

    # Summarise.
    local pass fail skip
    pass=$(grep -c $'^PASS\t' "$report" || true)
    fail=$(grep -c $'^FAIL\t' "$report" || true)
    skip=$(grep -c $'^SKIP\t' "$report" || true)

    {
        echo
        echo "## Summary"
        echo
        echo "- PASS: $pass"
        echo "- FAIL: $fail"
        echo "- SKIP: $skip"
        echo
        if (( fail > 0 )); then
            echo "## Overall: FAIL"
        else
            echo "## Overall: PASS"
        fi
    } >> "$report"

    info "report: $report  (PASS=$pass FAIL=$fail SKIP=$skip)"

    if [[ "$KEEP" != "1" ]]; then
        info "deleting $inst"
        incus delete --force "$inst" >/dev/null 2>&1 || true
        # Trim from active list so the trap doesn't double-delete.
        local pruned=()
        for i in "${ACTIVE_INSTANCES[@]}"; do
            [[ "$i" == "$inst" ]] || pruned+=("$i")
        done
        ACTIVE_INSTANCES=("${pruned[@]}")
    fi

    return $(( fail > 0 ? 1 : 0 ))
}

# ── Main ──────────────────────────────────────────────────────────────────

main() {
    local -a targets=()
    local -a positional=()

    # Single-pass argument parse: --flags bubble up to the top-level state,
    # positional args become the target list.
    while (( $# > 0 )); do
        case "$1" in
            --refresh-images)
                REFRESH_IMAGES=1
                shift
                ;;
            --help|-h)
                sed -n '/^# Usage:/,/^# Requirements/p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
                exit 0
                ;;
            --)
                shift
                while (( $# > 0 )); do positional+=("$1"); shift; done
                ;;
            --*)
                err "unknown flag: $1"
                exit 2
                ;;
            *)
                positional+=("$1")
                shift
                ;;
        esac
    done

    if (( ${#positional[@]} == 0 )) || [[ "${positional[0]:-}" == "all" ]]; then
        targets=("${ALL_TARGETS[@]}")
    else
        targets=("${positional[@]}")
    fi

    preflight
    build_tarball
    start_http

    local overall_fail=0
    for t in "${targets[@]}"; do
        if ! run_target "$t"; then
            overall_fail=1
        fi
    done

    if (( overall_fail == 0 )); then
        info "all targets PASS"
    else
        err "one or more targets FAILED — see reports in $HARNESS_DIR/"
    fi
    exit "$overall_fail"
}

main "$@"
