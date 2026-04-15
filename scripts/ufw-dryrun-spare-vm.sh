#!/usr/bin/env bash
# scripts/ufw-dryrun-spare-vm.sh — spare-VM dry run for validation/ufw-incus.rules
#
# Applies validation/ufw-incus.rules to a DISPOSABLE spare VM over SSH,
# enables UFW there, exercises the two smoke cases Phoenix needs
# evidence on before flipping UFW live on the dev host:
#
#   1. POSITIVE — tcp/9999 reachable from this host under UFW active.
#                 Required by .claude/rules/usability.md (the zero-terminal
#                 promise: the install-script URL must work from the LAN
#                 without further fiddling).
#   2. NEGATIVE — an un-listed port is dropped, confirming default-deny
#                 is actually in effect.
#
# Then tears the VM back to pristine (`ufw --force disable`,
# `ufw --force reset`) so it can be reused or deleted.
#
# Never touches the dev host. The --confirm-not-dev-host flag exits
# non-zero if the target resolves to this host's hostname or any of its
# local IPv4 addresses (including Tailscale).
#
# Usage (idempotent; rerun is safe):
#
#   scripts/ufw-dryrun-spare-vm.sh \
#       --host spare-vm.tail00.ts.net \
#       --ssh-user root \
#       --confirm-not-dev-host
#
# Exit codes:
#   0  all checks PASS
#   2  argparse / --confirm-not-dev-host missing
#   3  safety gate tripped (target is the dev host)
#   4  baseline curl failed before UFW was even enabled (VM setup bad)
#   5  UFW-active curl to :9999 blocked (rules are incomplete; see #97)

set -euo pipefail

# ── Argument parsing ───────────────────────────────────────────────────────

VM_HOST=""
SSH_USER="root"
CONFIRM_NOT_DEV=0
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

usage() {
    sed -n '/^# scripts\/ufw-dryrun-spare-vm.sh/,/^set -euo/p' "$0" \
        | sed 's/^# \{0,1\}//' | head -n -1
    exit 2
}

while (( $# > 0 )); do
    case "$1" in
        --host=*)                VM_HOST="${1#*=}"; shift ;;
        --host)                  VM_HOST="${2:-}"; shift 2 ;;
        --ssh-user=*)            SSH_USER="${1#*=}"; shift ;;
        --ssh-user)              SSH_USER="${2:-}"; shift 2 ;;
        --confirm-not-dev-host)  CONFIRM_NOT_DEV=1; shift ;;
        -h|--help)               usage ;;
        *)                       echo "unknown arg: $1" >&2; exit 2 ;;
    esac
done

[[ -n "$VM_HOST" ]] || { echo "error: --host=<spare-vm> required" >&2; exit 2; }
(( CONFIRM_NOT_DEV == 1 )) || {
    echo "error: --confirm-not-dev-host required (safety gate)" >&2
    exit 2
}

# ── Safety gate: refuse to run against the dev host ────────────────────────

DEV_HOSTNAME="$(hostname -f 2>/dev/null || hostname)"

resolve_one() {
    local h="$1"
    getent ahosts "$h" 2>/dev/null | awk '/ STREAM /{print $1; exit}' \
        || getent hosts "$h" 2>/dev/null | awk '{print $1; exit}' \
        || echo "$h"
}
VM_RESOLVED="$(resolve_one "$VM_HOST")"
[[ -n "$VM_RESOLVED" ]] || VM_RESOLVED="$VM_HOST"

collect_local_ips() {
    hostname -I 2>/dev/null || true
    ip -4 -o addr 2>/dev/null | awk '{print $4}' | cut -d/ -f1 || true
    command -v tailscale >/dev/null 2>&1 && tailscale ip -4 2>/dev/null || true
}

if [[ "$VM_RESOLVED" == "$DEV_HOSTNAME" ]] \
    || [[ "$VM_HOST" == "$DEV_HOSTNAME" ]] \
    || [[ "$VM_HOST" == "localhost" ]] \
    || [[ "$VM_RESOLVED" == "127.0.0.1" ]]; then
    echo "REFUSE: target $VM_HOST resolves to this dev host" >&2
    exit 3
fi

while IFS= read -r myip; do
    [[ -z "$myip" ]] && continue
    if [[ "$VM_RESOLVED" == "$myip" ]] || [[ "$VM_HOST" == "$myip" ]]; then
        echo "REFUSE: target $VM_HOST ($VM_RESOLVED) is a local IP of this dev host ($myip)" >&2
        exit 3
    fi
done < <(collect_local_ips)

# ── SSH helpers ────────────────────────────────────────────────────────────

SSH_OPTS=(-o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new)
ssh_vm() { ssh "${SSH_OPTS[@]}" "${SSH_USER}@${VM_HOST}" "$@"; }
scp_vm() { scp "${SSH_OPTS[@]}" "$@"; }

# Hostname double-check: comparing resolved IPs is not enough on split-horizon
# DNS or when Tailscale assigns the same short name to multiple machines.
REMOTE_HOSTNAME="$(ssh_vm 'hostname -f 2>/dev/null || hostname' 2>/dev/null || true)"
if [[ -z "$REMOTE_HOSTNAME" ]]; then
    echo "REFUSE: could not ssh to $VM_HOST (verify spare-VM is up and keys are trusted)" >&2
    exit 3
fi
if [[ "$REMOTE_HOSTNAME" == "$DEV_HOSTNAME" ]]; then
    echo "REFUSE: remote hostname ($REMOTE_HOSTNAME) matches dev hostname ($DEV_HOSTNAME)" >&2
    exit 3
fi

echo "[spare-vm] target: $VM_HOST ($VM_RESOLVED), remote-hostname: $REMOTE_HOSTNAME"
echo "[spare-vm] dev host: $DEV_HOSTNAME — confirmed distinct"

# ── Cleanup trap — runs on every exit path ─────────────────────────────────

CLEANUP_DONE=0
cleanup() {
    (( CLEANUP_DONE == 1 )) && return 0
    CLEANUP_DONE=1
    echo "[spare-vm] cleanup: reset UFW + stop :9999 stub"
    ssh_vm 'ufw --force disable >/dev/null 2>&1 || true
            ufw --force reset   >/dev/null 2>&1 || true
            pkill -f "python3 -m http.server 9999" 2>/dev/null || true
            rm -rf /tmp/ufw-smoke /tmp/ufw-incus.rules 2>/dev/null || true' || true
}
trap cleanup EXIT INT TERM

# ── Apply rules ────────────────────────────────────────────────────────────

echo "[spare-vm] pushing validation/ufw-incus.rules"
scp_vm "$REPO_ROOT/validation/ufw-incus.rules" "${SSH_USER}@${VM_HOST}:/tmp/ufw-incus.rules"

echo "[spare-vm] ensuring ufw + python3 + curl present on VM"
ssh_vm '
    set -e
    command -v ufw     >/dev/null 2>&1 || { apt-get update -qq && apt-get install -y -qq ufw; }
    command -v python3 >/dev/null 2>&1 || { apt-get update -qq && apt-get install -y -qq python3; }
    command -v curl    >/dev/null 2>&1 || { apt-get update -qq && apt-get install -y -qq curl; }
'

echo "[spare-vm] staging rules (UFW still inactive)"
ssh_vm 'bash /tmp/ufw-incus.rules'

# Start a trivial :9999 listener to stand in for ventd.
echo "[spare-vm] starting stand-in listener on :9999"
ssh_vm '
    mkdir -p /tmp/ufw-smoke
    echo "ok" > /tmp/ufw-smoke/index.html
    pkill -f "python3 -m http.server 9999" 2>/dev/null || true
    nohup python3 -m http.server 9999 --directory /tmp/ufw-smoke >/tmp/ufw-smoke/srv.log 2>&1 &
    sleep 1
'

curl_vm() { curl -sfm 5 -o /dev/null -w '%{http_code}' "$@" 2>/dev/null; }

# ── Baseline (UFW inactive) — positive control ─────────────────────────────

echo "[spare-vm] baseline: curl VM:9999 (UFW still OFF)"
PRE_CODE="$(curl_vm "http://${VM_HOST}:9999/index.html" || echo 000)"
if [[ "$PRE_CODE" != "200" ]]; then
    echo "FAIL (exit 4): baseline curl to http://${VM_HOST}:9999 returned $PRE_CODE before UFW was enabled"
    echo "  VM network or stub listener is broken — fix the spare VM before re-running."
    exit 4
fi
echo "  baseline HTTP code: $PRE_CODE (expected 200)"

# ── Enable UFW on the spare VM only ────────────────────────────────────────

echo "[spare-vm] enabling UFW on VM (spare only — dev host untouched)"
ssh_vm 'ufw --force enable >/dev/null 2>&1'
ssh_vm 'ufw status verbose' | sed -n '1,30p' | sed 's/^/    /'

# ── Smoke under UFW active ─────────────────────────────────────────────────

echo "[spare-vm] under UFW active: curl VM:9999 (expected 200)"
POST_9999_CODE="$(curl_vm "http://${VM_HOST}:9999/index.html" || echo 000)"
if [[ "$POST_9999_CODE" == "200" ]]; then
    PASS_9999=1
    echo "  PASS  curl → VM:9999 under UFW active → $POST_9999_CODE"
else
    PASS_9999=0
    echo "  FAIL  curl → VM:9999 under UFW active → $POST_9999_CODE"
    echo "        rules do not allow tcp/9999 — see ventd/ventd#97"
fi

echo "[spare-vm] under UFW active: curl VM:8080 (expected: blocked → non-200)"
BLOCKED_CODE="$(curl_vm "http://${VM_HOST}:8080/" || echo 000)"
if [[ "$BLOCKED_CODE" == "200" ]]; then
    BLOCKED_OK=0
    echo "  WARN  curl → VM:8080 reached a listener — unexpected but does not by itself invalidate the deny default"
else
    BLOCKED_OK=1
    echo "  PASS  curl → VM:8080 dropped (HTTP $BLOCKED_CODE)"
fi

# ── Report ─────────────────────────────────────────────────────────────────

echo ""
echo "=========================================="
echo "UFW spare-VM dry-run report"
echo "  VM:                         $VM_HOST ($VM_RESOLVED)"
echo "  baseline (UFW off) → :9999: $PRE_CODE"
echo "  UFW on              → :9999: $POST_9999_CODE  (want 200)"
echo "  UFW on              → :8080: $BLOCKED_CODE   (want non-200)"
echo ""
if (( PASS_9999 == 1 )); then
    echo "VERDICT: rules are safe to promote to the dev host — LAN can still reach ventd on :9999."
else
    echo "VERDICT: rules ARE NOT safe to promote. LAN cannot reach ventd on :9999."
    echo "         Do NOT run 'sudo ufw enable' on the dev host. Merge ventd/ventd#97's fix first."
fi
echo "=========================================="

if (( PASS_9999 == 0 )); then
    exit 5
fi
exit 0
