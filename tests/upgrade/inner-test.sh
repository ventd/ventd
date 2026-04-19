#!/usr/bin/env bash
# inner-test.sh — runs INSIDE the Docker container for each distro.
#
# Responsibilities:
#   1. Install distro-specific dependencies.
#   2. Install ventd v0.2.0 via scripts/install.sh (VENTD_TEST_MODE=1).
#   3. Plant fixture config + calibration, generate bcrypt hash.
#   4. Start ventd v0.2.0 directly (no systemd), wait for health.
#   5. Stop v0.2.0; install v0.3.0-candidate binary.
#   6. Start v0.3.0-candidate, wait for health.
#   7. Run each gate assertion script; print PASS/FAIL per gate.
#
# Mounts expected from harness.sh:
#   /upgrade-tests/         — the tests/upgrade/ tree (read-only)
#   /upgrade-tests/candidate-binary — the v0.3.0 binary (read-only)
#   /upgrade-tests/repo/    — the ventd repo root (for scripts/install.sh)
#
# Output: each gate prints "[PASS] gate<N>-..." or "[FAIL] gate<N>-..."
# Exit code: 0 if all five gates PASS, non-zero if any FAIL.

set -euo pipefail

DISTRO_FAMILY="${DISTRO_FAMILY:-unknown}"
VENTD_PORT="19999"
ADMIN_PASS="TestUpgrade2025"
VENTD_ETC_DIR="/etc/ventd"
CANDIDATE_BINARY="/upgrade-tests/candidate-binary"
INSTALL_SH="/upgrade-tests/repo/scripts/install.sh"
FIXTURES_DIR="/upgrade-tests/fixtures"
ASSERTIONS_DIR="/upgrade-tests/assertions"

EXPECTED_HASH_FILE="/tmp/ventd-upgrade-expected-hash.txt"
PRE_UPGRADE_CURVES_JSON="/tmp/ventd-pre-upgrade-curves.json"
VENTD_LOG="/tmp/ventd-upgrade.log"
VENTD_PID_FILE="/tmp/ventd-upgrade.pid"

log()  { printf '[inner-test] %s\n' "$*" >&2; }
die()  { log "ERROR: $*"; exit 1; }

# ── 1. Install distro dependencies ──────────────────────────────────────────

log "distro family: $DISTRO_FAMILY"

install_deps() {
    case "$DISTRO_FAMILY" in
        ubuntu|debian)
            export DEBIAN_FRONTEND=noninteractive
            apt-get update -qq
            apt-get install -y -qq curl python3 python3-yaml python3-bcrypt jq
            ;;
        fedora|rhel|centos)
            dnf install -y -q curl python3 python3-pyyaml python3-bcrypt jq
            ;;
        arch)
            pacman -Sy --noconfirm --needed curl python python-yaml python-bcrypt jq
            ;;
        alpine)
            apk add --no-cache curl python3 py3-yaml py3-bcrypt jq
            ;;
        *)
            log "WARNING: unknown distro family '$DISTRO_FAMILY'; attempting apt-get fallback"
            apt-get update -qq 2>/dev/null || true
            apt-get install -y -qq curl python3 python3-yaml python3-bcrypt jq 2>/dev/null || true
            ;;
    esac
}

install_deps
log "deps installed"

# Verify python3 + yaml + bcrypt are working.
python3 -c "import yaml, bcrypt, json" || die "python3 deps check failed"

# ── 2. Install ventd v0.2.0 ─────────────────────────────────────────────────
#
# Uses scripts/install.sh with VENTD_TEST_MODE=1 which skips:
#   - root gate (we may be root already)
#   - systemctl/openrc/runit service activation
#   - ventd user/group creation
#   - hwmon module probe
#   - apparmor/selinux loader
# and downloads the v0.2.0 release binary from GitHub.

log "installing ventd v0.2.0 (VENTD_TEST_MODE=1)..."
VENTD_TEST_MODE=1 VENTD_VERSION=v0.2.0 bash "$INSTALL_SH" \
    2>&1 | tee /tmp/ventd-v020-install.log || die "v0.2.0 install failed"

[[ -x /usr/local/bin/ventd ]] || die "ventd binary not found at /usr/local/bin/ventd"

v020_ver=$(/usr/local/bin/ventd --version 2>/dev/null || echo "unknown")
log "v0.2.0 installed: $v020_ver"

# ── 3. Seed fixture state ────────────────────────────────────────────────────

log "seeding fixture state..."

# a. Generate bcrypt hash of the test password.
ADMIN_HASH=$(python3 -c "
import bcrypt, sys
password = sys.argv[1].encode('utf-8')
h = bcrypt.hashpw(password, bcrypt.gensalt(rounds=10))
print(h.decode('utf-8'))
" "$ADMIN_PASS")
echo "$ADMIN_HASH" > "$EXPECTED_HASH_FILE"
log "bcrypt hash generated (saved to $EXPECTED_HASH_FILE)"

# b. Plant config with the generated hash.
mkdir -p "$VENTD_ETC_DIR"
sed "s|ADMIN_HASH_PLACEHOLDER|${ADMIN_HASH}|" \
    "$FIXTURES_DIR/config.tmpl.yaml" > "$VENTD_ETC_DIR/config.yaml"
log "config.yaml planted at $VENTD_ETC_DIR/config.yaml"

# c. Plant calibration fixture.
cp "$FIXTURES_DIR/calibration.json" "$VENTD_ETC_DIR/calibration.json"
log "calibration.json planted"

# ── 4. Start ventd v0.2.0 ───────────────────────────────────────────────────
#
# Systemd is not available in Docker containers; we start ventd directly.
# This variance is documented in tests/upgrade/README.md.

log "starting ventd v0.2.0 directly (no systemd in container)..."
/usr/local/bin/ventd -config "$VENTD_ETC_DIR/config.yaml" \
    > "$VENTD_LOG" 2>&1 &
echo $! > "$VENTD_PID_FILE"
log "ventd PID: $(cat "$VENTD_PID_FILE")"

# Wait for /api/ping.
healthy=0
for i in $(seq 1 30); do
    code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${VENTD_PORT}/api/ping" 2>/dev/null || true)
    if [[ "$code" == "200" ]]; then
        healthy=1
        log "v0.2.0 healthy after ${i}s"
        break
    fi
    sleep 1
done

if [[ "$healthy" -ne 1 ]]; then
    log "v0.2.0 log tail:"
    tail -n 20 "$VENTD_LOG" >&2 || true
    die "v0.2.0 did not become healthy within 30s"
fi

# Save pre-upgrade GET /api/config response for gate 3 comparison.
COOKIE_JAR="$(mktemp -t ventd-inner-cookies-XXXX.txt)"
login_code=$(curl -s -o /dev/null -w '%{http_code}' \
    -c "$COOKIE_JAR" \
    -X POST \
    -d "password=${ADMIN_PASS}" \
    "http://127.0.0.1:${VENTD_PORT}/login" 2>/dev/null || true)
if [[ "$login_code" == "200" || "$login_code" == "302" || "$login_code" == "303" ]]; then
    curl -s -o "$PRE_UPGRADE_CURVES_JSON" \
        -b "$COOKIE_JAR" \
        "http://127.0.0.1:${VENTD_PORT}/api/config" 2>/dev/null || true
    log "pre-upgrade /api/config saved (login HTTP $login_code)"
else
    log "WARNING: pre-upgrade login returned HTTP $login_code; gate3 will use fixture values"
fi
rm -f "$COOKIE_JAR"

# ── 5. Stop v0.2.0; install v0.3.0-candidate ────────────────────────────────

log "stopping ventd v0.2.0..."
ventd_pid=$(cat "$VENTD_PID_FILE")
kill "$ventd_pid" 2>/dev/null || true
# Wait for clean exit (up to 10s).
for _ in $(seq 1 10); do
    kill -0 "$ventd_pid" 2>/dev/null || break
    sleep 1
done
kill -9 "$ventd_pid" 2>/dev/null || true
wait "$ventd_pid" 2>/dev/null || true
log "v0.2.0 stopped"

# Install v0.3.0-candidate: run install.sh with the candidate binary path.
# This exercises the full upgrade install path (unit file refresh, etc.)
# while VENTD_TEST_MODE=1 skips service activation.
log "installing v0.3.0-candidate (VENTD_TEST_MODE=1)..."
VENTD_TEST_MODE=1 bash "$INSTALL_SH" "$CANDIDATE_BINARY" \
    2>&1 | tee /tmp/ventd-candidate-install.log || die "v0.3.0-candidate install failed"

v030_ver=$(/usr/local/bin/ventd --version 2>/dev/null || echo "unknown")
log "v0.3.0-candidate installed: $v030_ver"

# ── 6. Start v0.3.0-candidate ───────────────────────────────────────────────

log "starting ventd v0.3.0-candidate..."
/usr/local/bin/ventd -config "$VENTD_ETC_DIR/config.yaml" \
    >> "$VENTD_LOG" 2>&1 &
echo $! > "$VENTD_PID_FILE"
log "ventd PID: $(cat "$VENTD_PID_FILE")"

healthy=0
for i in $(seq 1 30); do
    code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${VENTD_PORT}/api/ping" 2>/dev/null || true)
    if [[ "$code" == "200" ]]; then
        healthy=1
        log "v0.3.0-candidate healthy after ${i}s"
        break
    fi
    sleep 1
done

if [[ "$healthy" -ne 1 ]]; then
    log "v0.3.0-candidate log tail:"
    tail -n 30 "$VENTD_LOG" >&2 || true
    die "v0.3.0-candidate did not become healthy within 30s"
fi

# ── 7. Run gate assertions ───────────────────────────────────────────────────

log "running gate assertions..."

overall_rc=0
GATE_RESULTS=""

run_gate() {
    local script="$1"
    local name
    name=$(basename "$script" .sh)
    if VENTD_PORT="$VENTD_PORT" \
       VENTD_PASS="$ADMIN_PASS" \
       VENTD_ETC_DIR="$VENTD_ETC_DIR" \
       EXPECTED_HASH_FILE="$EXPECTED_HASH_FILE" \
       EXPECTED_CURVE_JSON="${PRE_UPGRADE_CURVES_JSON}" \
       FIXTURE_HAS_DYNAMIC_REBIND="true" \
       FIXTURE_SCHEMA_VERSION="2" \
       EXPECTED_CURVE_NAME="cpu_linear" \
       bash "$script" 2>&1; then
        GATE_RESULTS="${GATE_RESULTS}PASS:${name}|"
    else
        GATE_RESULTS="${GATE_RESULTS}FAIL:${name}|"
        overall_rc=1
    fi
}

run_gate "$ASSERTIONS_DIR/gate1-config.sh"
run_gate "$ASSERTIONS_DIR/gate2-calibration.sh"
run_gate "$ASSERTIONS_DIR/gate3-curves.sh"
run_gate "$ASSERTIONS_DIR/gate4-bcrypt.sh"
run_gate "$ASSERTIONS_DIR/gate5-no-wizard.sh"

# ── Stop ventd ───────────────────────────────────────────────────────────────

ventd_pid=$(cat "$VENTD_PID_FILE" 2>/dev/null || true)
if [[ -n "$ventd_pid" ]]; then
    kill "$ventd_pid" 2>/dev/null || true
    wait "$ventd_pid" 2>/dev/null || true
fi

# ── Print summary ────────────────────────────────────────────────────────────

echo ""
echo "═══════════════════════════════════════"
echo "DISTRO: $DISTRO_FAMILY"
echo "GATES:  $GATE_RESULTS"
echo "RESULT: $([ "$overall_rc" -eq 0 ] && echo PASS || echo FAIL)"
echo "═══════════════════════════════════════"

exit "$overall_rc"
