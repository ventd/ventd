#!/usr/bin/env bash
# tests/upgrade/harness.sh — upgrade-path test harness driver.
#
# Runs the v0.2.0 → v0.3.0-candidate upgrade-path test for one distro inside
# a Docker container. Each of the five acceptance gates in issue #183 is
# checked; results are printed as PASS/FAIL per gate.
#
# Usage:
#   bash tests/upgrade/harness.sh <distro-tag> [OPTIONS]
#
# Distro tags:
#   ubuntu:24.04   fedora:41   archlinux:latest   alpine:3.20
#
# Options:
#   --candidate-binary <path>   Path to the v0.3.0-candidate ventd binary.
#                               If omitted, the harness builds one from the
#                               current repo tree (requires Go).
#   --keep-on-failure           Leave the container running on gate failure
#                               for manual inspection (docker exec).
#   --log-dir <path>            Directory to write per-distro logs.
#                               Default: /tmp/ventd-upgrade-<distro>-<ts>/
#
# Examples:
#   # Run against ubuntu:24.04 using a pre-built binary:
#   bash tests/upgrade/harness.sh ubuntu:24.04 --candidate-binary /tmp/ventd
#
#   # Build the candidate from the current tree and test all four distros:
#   for d in ubuntu:24.04 fedora:41 archlinux:latest alpine:3.20; do
#       bash tests/upgrade/harness.sh "$d"
#   done
#
# CI usage (see .github/workflows/upgrade-path.yml):
#   CANDIDATE_BINARY=/path/to/ventd bash tests/upgrade/harness.sh ubuntu:24.04

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# ── Argument parsing ─────────────────────────────────────────────────────────

DISTRO=""
CANDIDATE_BINARY="${CANDIDATE_BINARY:-}"
KEEP_ON_FAILURE=0
LOG_DIR=""

usage() {
    sed -n '2,/^set -/p' "$0" | grep -v '^set -' >&2
    exit 2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --candidate-binary)  CANDIDATE_BINARY="$2"; shift 2 ;;
        --keep-on-failure)   KEEP_ON_FAILURE=1;      shift   ;;
        --log-dir)           LOG_DIR="$2";           shift 2 ;;
        -h|--help)           usage ;;
        -*)                  echo "unknown option: $1" >&2; usage ;;
        *)
            if [[ -z "$DISTRO" ]]; then DISTRO="$1"; shift
            else echo "unexpected argument: $1" >&2; usage; fi
            ;;
    esac
done

[[ -n "$DISTRO" ]] || { echo "error: distro argument required" >&2; usage; }

# ── Distro family detection ──────────────────────────────────────────────────

case "$DISTRO" in
    ubuntu:*|debian:*)       DISTRO_FAMILY="ubuntu" ;;
    fedora:*|centos:*)       DISTRO_FAMILY="fedora" ;;
    archlinux:*|arch:*)      DISTRO_FAMILY="arch"   ;;
    alpine:*)                DISTRO_FAMILY="alpine" ;;
    *)
        echo "error: unsupported distro tag: $DISTRO" >&2
        echo "  supported: ubuntu:24.04, fedora:41, archlinux:latest, alpine:3.20" >&2
        exit 2
        ;;
esac

log()  { printf '[harness] %s\n' "$*" >&2; }
die()  { log "ERROR: $*"; exit 1; }

# ── Candidate binary ─────────────────────────────────────────────────────────

if [[ -z "$CANDIDATE_BINARY" ]]; then
    log "no --candidate-binary provided; building from $REPO_ROOT ..."
    CANDIDATE_BINARY="$(mktemp -t ventd-candidate-XXXX)"
    ( cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$CANDIDATE_BINARY" ./cmd/ventd ) || \
        die "go build failed; pass --candidate-binary to skip the build"
    chmod +x "$CANDIDATE_BINARY"
    log "built candidate: $CANDIDATE_BINARY"
fi

[[ -f "$CANDIDATE_BINARY" ]] || die "candidate binary not found: $CANDIDATE_BINARY"
[[ -x "$CANDIDATE_BINARY" ]] || die "candidate binary is not executable: $CANDIDATE_BINARY"

# Verify the binary is for linux/amd64 (most container images are amd64).
if command -v file >/dev/null 2>&1; then
    file_out=$(file "$CANDIDATE_BINARY")
    if ! echo "$file_out" | grep -qiE "ELF.*x86.64|ELF.*amd64"; then
        log "WARNING: candidate binary may not be linux/amd64: $file_out"
    fi
fi

# ── Log directory ─────────────────────────────────────────────────────────────

TS="$(date +%Y%m%d-%H%M%S)"
DISTRO_SLUG="${DISTRO//:/-}"
if [[ -z "$LOG_DIR" ]]; then
    LOG_DIR="$(mktemp -d -t "ventd-upgrade-${DISTRO_SLUG}-${TS}-XXXX")"
fi
mkdir -p "$LOG_DIR"
log "logs → $LOG_DIR"

# ── Docker check ─────────────────────────────────────────────────────────────

command -v docker >/dev/null 2>&1 || die "docker not found; install Docker to run the upgrade harness"

# ── Launch container ─────────────────────────────────────────────────────────
#
# Mounts:
#   /upgrade-tests/                  — tests/upgrade/ tree (fixtures, assertions, inner-test.sh)
#   /upgrade-tests/candidate-binary  — the v0.3.0-candidate binary
#   /upgrade-tests/repo/             — repo root (for scripts/install.sh)
#
# Systemd is NOT available inside the container; inner-test.sh runs ventd
# directly as a background process. This is documented in README.md.
#
# --privileged is needed so the container can bind to a port and access
# certain system calls that ventd uses at startup (sysfs, ioctl). In practice
# most calls fail gracefully (ENOENT / EPERM), but CAP_NET_BIND_SERVICE is
# required for binding to port 19999 and a few hwmon probes need elevated caps.

CONTAINER_NAME="ventd-upgrade-${DISTRO_SLUG}-${TS}"

# shellcheck disable=SC2317  # called via trap
docker_cleanup() {
    local rc=$?
    set +e
    if [[ "$rc" -ne 0 && "$KEEP_ON_FAILURE" -eq 1 ]]; then
        log "KEEP_ON_FAILURE=1 — leaving container '$CONTAINER_NAME' running for inspection"
        log "  docker exec -it $CONTAINER_NAME bash"
    else
        docker rm -f "$CONTAINER_NAME" >/dev/null 2>&1 || true
    fi
    exit "$rc"
}
trap docker_cleanup EXIT INT TERM

log "pulling image $DISTRO ..."
docker pull "$DISTRO" >/dev/null 2>&1 || log "WARNING: docker pull failed; using cached image"

log "starting container $CONTAINER_NAME ..."
docker run -d \
    --name "$CONTAINER_NAME" \
    --privileged \
    -v "${SCRIPT_DIR}:/upgrade-tests:ro" \
    -v "${CANDIDATE_BINARY}:/upgrade-tests/candidate-binary:ro" \
    -v "${REPO_ROOT}:/upgrade-tests/repo:ro" \
    "$DISTRO" \
    sleep 600

# ── Run inner test ────────────────────────────────────────────────────────────

log "running inner-test.sh inside $CONTAINER_NAME ..."
set +e
docker exec \
    -e "DISTRO_FAMILY=${DISTRO_FAMILY}" \
    "$CONTAINER_NAME" \
    bash /upgrade-tests/inner-test.sh \
    2>&1 | tee "$LOG_DIR/inner-test.log"
inner_rc=${PIPESTATUS[0]}
set -e

# ── Collect logs from container ───────────────────────────────────────────────

for logfile in /tmp/ventd-upgrade.log /tmp/ventd-v020-install.log /tmp/ventd-candidate-install.log; do
    dest="$LOG_DIR/$(basename "$logfile")"
    docker exec "$CONTAINER_NAME" cat "$logfile" > "$dest" 2>/dev/null || true
done

# ── Print result ──────────────────────────────────────────────────────────────

echo ""
if [[ "$inner_rc" -eq 0 ]]; then
    printf '[harness] PASS: %s — all five gates passed\n' "$DISTRO"
else
    printf '[harness] FAIL: %s — one or more gates failed (logs: %s)\n' "$DISTRO" "$LOG_DIR"
fi

exit "$inner_rc"
