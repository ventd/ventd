#!/usr/bin/env bash
# ventd fresh-VM install smoke — Proxmox driver.
#
# Clones a cloud-init template on a Proxmox host, drives a fresh install of
# ventd over HTTP from this host, then runs assertion collection in-guest and
# pulls the result JSON back. Guarantees the disposable VM is destroyed on
# every exit path.
#
# Invoked once per distro. See matrix.sh for the multi-distro sweep.
#
# Prerequisites (see README.md): SSH key trust to the Proxmox host as root,
# one cloud-init template per distro, snippets enabled on the target storage.

set -euo pipefail

# ── Defaults ────────────────────────────────────────────────────────────────

PVE_HOST="${PVE_HOST:-root@pve}"
PVE_STORAGE="${PVE_STORAGE:-local}"          # snippet storage name on PVE
PVE_DISK_STORAGE="${PVE_DISK_STORAGE:-local-lvm}"
VMID_START="${VMID_START:-9010}"             # first disposable VMID to try
VMID_END="${VMID_END:-9099}"                 # last disposable VMID to try
HTTP_PORT="${HTTP_PORT:-8765}"
HTTP_BIND="${HTTP_BIND:-0.0.0.0}"
GUEST_TIMEOUT="${GUEST_TIMEOUT:-300}"        # seconds to wait for guest agent + DONE marker

DISTRO=""
BINARY=""
INSTALL_SH=""
LAN_IP=""
KEEP_ON_FAILURE="${KEEP_ON_FAILURE:-0}"
RESULT_DIR=""

# ── Distro → template VMID + snippet filename ───────────────────────────────

template_for() {
    case "$1" in
        ubuntu-24.04) echo 9000 ;;
        debian-12)    echo 9001 ;;
        fedora-40)    echo 9002 ;;
        arch)         echo 9003 ;;
        *) return 1 ;;
    esac
}

snippet_for() {
    case "$1" in
        ubuntu-24.04) echo ventd-smoke-ubuntu-2404.yaml ;;
        debian-12)    echo ventd-smoke-debian-12.yaml ;;
        fedora-40)    echo ventd-smoke-fedora-40.yaml ;;
        arch)         echo ventd-smoke-arch.yaml ;;
        *) return 1 ;;
    esac
}

# ── Logging ─────────────────────────────────────────────────────────────────

log()  { printf '[%s] %s\n'   "$(date +%H:%M:%S)" "$*" >&2; }
die()  { log "ERROR: $*"; exit 1; }

# ── Argument parsing ────────────────────────────────────────────────────────

usage() {
    cat <<'EOF'
Usage: run.sh --distro <name> --binary <path> [--install-sh <path>] [--lan-ip <ip>]
              [--pve-host <user@host>] [--vmid-start N] [--result-dir <dir>]
              [--keep-on-failure]

  --distro         ubuntu-24.04 | debian-12 | fedora-40 | arch
  --binary         Path to a locally-built ventd binary
  --install-sh     Path to scripts/install.sh (default: ../../scripts/install.sh)
  --lan-ip         Dev-host IP the VMs can reach (autodetected if omitted)
  --pve-host       Proxmox host for ssh (default: root@pve)
  --vmid-start     First disposable VMID to try (default: 9010)
  --result-dir     Where to dump result JSON + install log (default: mktemp)
  --keep-on-failure  Leave the VM alive on assertion failure for debugging
EOF
    exit 2
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --distro)          DISTRO="$2"; shift 2 ;;
        --binary)          BINARY="$2"; shift 2 ;;
        --install-sh)      INSTALL_SH="$2"; shift 2 ;;
        --lan-ip)          LAN_IP="$2"; shift 2 ;;
        --pve-host)        PVE_HOST="$2"; shift 2 ;;
        --vmid-start)      VMID_START="$2"; shift 2 ;;
        --result-dir)      RESULT_DIR="$2"; shift 2 ;;
        --keep-on-failure) KEEP_ON_FAILURE=1; shift ;;
        -h|--help)         usage ;;
        *) die "unknown arg: $1" ;;
    esac
done

[[ -n "$DISTRO" ]] || usage
[[ -n "$BINARY" ]] || die "--binary required"
[[ -f "$BINARY" ]] || die "binary not found: $BINARY"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
if [[ -z "$INSTALL_SH" ]]; then
    INSTALL_SH="$REPO_ROOT/scripts/install.sh"
fi
[[ -f "$INSTALL_SH" ]] || die "install.sh not found: $INSTALL_SH"

SNIPPET_FILE="$(snippet_for "$DISTRO")" || die "unsupported distro: $DISTRO"
TEMPLATE_VMID="$(template_for "$DISTRO")" || die "unsupported distro: $DISTRO"
SNIPPET_SRC="$SCRIPT_DIR/snippets/$SNIPPET_FILE"
[[ -f "$SNIPPET_SRC" ]] || die "snippet missing: $SNIPPET_SRC"

if [[ -z "$RESULT_DIR" ]]; then
    RESULT_DIR="$(mktemp -d -t ventd-smoke-XXXX)"
fi
mkdir -p "$RESULT_DIR"

# ── Remote PVE helper ───────────────────────────────────────────────────────

PVE() { ssh -o BatchMode=yes -o ConnectTimeout=10 "$PVE_HOST" "$@"; }
PVE_SCP() { scp -o BatchMode=yes -o ConnectTimeout=10 "$@"; }

# ── LAN IP autodetection ────────────────────────────────────────────────────
#
# We need a dev-host IP reachable from the VM bridge on pve. SSH_CLIENT is
# unreliable — when pve is contacted over tailscale or via another overlay,
# SSH_CLIENT points at the overlay address, which the VMs cannot route back
# to. Instead, read pve's vmbr0 address + prefix, then find a local interface
# in that same subnet. Fall back to --lan-ip if no match.

if [[ -z "$LAN_IP" ]]; then
    PVE_BRIDGE="${PVE_BRIDGE:-vmbr0}"
    PVE_CIDR="$(PVE "ip -o -4 addr show $PVE_BRIDGE 2>/dev/null | awk '{print \$4; exit}'")"
    if [[ -n "$PVE_CIDR" ]]; then
        # Pick the first local IPv4 that sits inside pve's bridge subnet.
        LAN_IP="$(python3 -c "
import ipaddress, json, subprocess, sys
net = ipaddress.ip_network('$PVE_CIDR', strict=False)
out = subprocess.check_output(['ip','-j','-4','addr']).decode()
for iface in json.loads(out):
    for a in iface.get('addr_info', []):
        ip = a.get('local')
        if not ip: continue
        try:
            if ipaddress.ip_address(ip) in net:
                print(ip); sys.exit(0)
        except ValueError:
            pass
sys.exit(1)
" 2>/dev/null)"
    fi
    if [[ -z "$LAN_IP" ]]; then
        # Fall back to SSH_CLIENT (works when pve and dev are on one LAN).
        LAN_IP="$(PVE "echo \$SSH_CLIENT | awk '{print \$1}'")"
    fi
    [[ -n "$LAN_IP" ]] || die "could not autodetect LAN IP reachable from $PVE_BRIDGE; pass --lan-ip"
fi
log "dev-host LAN IP: $LAN_IP"

# ── State for cleanup ───────────────────────────────────────────────────────

HTTP_PID=""
SERVE_DIR=""
VMID=""
REMOTE_SNIPPET=""
ASSERTIONS_PASSED=0

cleanup() {
    local rc=$?
    set +e

    if [[ -n "$VMID" ]]; then
        if [[ "$rc" -ne 0 && "$KEEP_ON_FAILURE" == "1" ]]; then
            log "KEEP_ON_FAILURE=1 — leaving VM $VMID alive for inspection"
        else
            log "tearing down VM $VMID"
            PVE "qm stop $VMID --skiplock" >/dev/null 2>&1 || true
            # Short wait for the stop to settle before destroy.
            for _ in 1 2 3 4 5; do
                PVE "qm status $VMID" 2>&1 | grep -q 'status: stopped' && break
                sleep 1
            done
            PVE "qm destroy $VMID --purge" >/dev/null 2>&1 || true
        fi
    fi

    if [[ -n "$REMOTE_SNIPPET" ]]; then
        PVE "rm -f '$REMOTE_SNIPPET'" >/dev/null 2>&1 || true
    fi

    if [[ -n "$HTTP_PID" ]]; then
        kill "$HTTP_PID" 2>/dev/null || true
        for _ in 1 2 3 4 5; do
            kill -0 "$HTTP_PID" 2>/dev/null || break
            sleep 0.2
        done
        kill -9 "$HTTP_PID" 2>/dev/null || true
        wait "$HTTP_PID" 2>/dev/null || true
    fi

    if [[ -n "$SERVE_DIR" && -d "$SERVE_DIR" ]]; then
        rm -rf "$SERVE_DIR"
    fi

    exit "$rc"
}
trap cleanup EXIT INT TERM

# ── Serve a release-style tarball over HTTP ────────────────────────────────
#
# install.sh looks for sibling assets by relative path
# (scripts/_ventd_account.sh, deploy/ventd.service, deploy/90-ventd-hwmon.rules,
# etc.). Serving just install.sh + the binary makes it exit 1 on "ventd.service
# not found". Mirror the release-tarball layout instead:
#
#   $SERVE_DIR/bundle.tar.gz
#     ventd                              (binary)
#     scripts/install.sh                 (the real installer)
#     scripts/_ventd_account.sh
#     scripts/ventd.openrc
#     scripts/ventd.runit
#     deploy/ventd.service
#     deploy/ventd-recover.service
#     deploy/90-ventd-hwmon.rules
#     deploy/apparmor.d/usr.local.bin.ventd
#     deploy/selinux/*
#
# Cloud-init curls bundle.tar.gz, extracts, runs scripts/install.sh. install.sh
# auto-discovers the binary via $SCRIPT_DIR/../ventd and walks the same
# relative paths it would in a normal release tarball.

SERVE_DIR="$(mktemp -d -t ventd-smoke-serve-XXXX)"
BUNDLE_ROOT="$SERVE_DIR/bundle"
mkdir -p "$BUNDLE_ROOT/scripts" "$BUNDLE_ROOT/deploy"
install -m 0755 "$BINARY" "$BUNDLE_ROOT/ventd"
install -m 0755 "$INSTALL_SH" "$BUNDLE_ROOT/scripts/install.sh"
for f in _ventd_account.sh ventd.openrc ventd.runit; do
    if [[ -f "$REPO_ROOT/scripts/$f" ]]; then
        install -m 0755 "$REPO_ROOT/scripts/$f" "$BUNDLE_ROOT/scripts/$f"
    fi
done
cp -a "$REPO_ROOT/deploy/." "$BUNDLE_ROOT/deploy/"
tar -C "$SERVE_DIR/bundle" -czf "$SERVE_DIR/bundle.tar.gz" .
rm -rf "$BUNDLE_ROOT"

log "starting HTTP server on $HTTP_BIND:$HTTP_PORT (serving $SERVE_DIR)"
# Refuse to start if the port is already held — a previous aborted run can
# leave a python http.server alive; silently binding to a different port
# (or failing to bind and continuing with a stale server) would make the
# next smoke run falsely fail with 404s.
if ss -Hltn "sport = :$HTTP_PORT" 2>/dev/null | grep -q .; then
    die "port $HTTP_PORT already in use — stop the old process first (pgrep -af http.server)"
fi
python3 -m http.server --bind "$HTTP_BIND" --directory "$SERVE_DIR" "$HTTP_PORT" \
    >"$RESULT_DIR/http.log" 2>&1 &
HTTP_PID=$!
# Smoke test the server is accepting locally first.
http_ready=0
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    if curl -sSf "http://127.0.0.1:${HTTP_PORT}/bundle.tar.gz" -o /dev/null 2>/dev/null; then
        http_ready=1
        break
    fi
    sleep 0.2
done
(( http_ready == 1 )) || die "HTTP server did not come up on :$HTTP_PORT (see $RESULT_DIR/http.log)"

BUNDLE_URL="http://${LAN_IP}:${HTTP_PORT}/bundle.tar.gz"
log "bundle URL: $BUNDLE_URL"

# ── Pick a free disposable VMID ─────────────────────────────────────────────

pick_vmid() {
    local v in_use
    in_use="$(PVE "qm list --full 2>/dev/null | awk 'NR>1 {print \$1}'")"
    for v in $(seq "$VMID_START" "$VMID_END"); do
        if ! grep -q -x "$v" <<<"$in_use"; then
            echo "$v"
            return 0
        fi
    done
    return 1
}

VMID="$(pick_vmid)" || die "no free VMID in ${VMID_START}-${VMID_END}"
log "allocated VMID: $VMID"

# ── Materialize snippet + upload to PVE ─────────────────────────────────────

RESULT_PATH_IN_GUEST="/tmp/ventd-smoke-result.json"
LOCAL_SNIPPET="$RESULT_DIR/${SNIPPET_FILE}"
sed \
    -e "s|%BUNDLE_URL%|${BUNDLE_URL}|g" \
    -e "s|%RESULT_PATH%|${RESULT_PATH_IN_GUEST}|g" \
    "$SNIPPET_SRC" > "$LOCAL_SNIPPET"

REMOTE_SNIPPET_NAME="ventd-smoke-${DISTRO}-${VMID}.yaml"
REMOTE_SNIPPET="/var/lib/vz/snippets/${REMOTE_SNIPPET_NAME}"
log "uploading snippet: $REMOTE_SNIPPET_NAME"
PVE_SCP "$LOCAL_SNIPPET" "${PVE_HOST}:${REMOTE_SNIPPET}" >/dev/null

# ── Clone template + attach snippet + start ─────────────────────────────────

CLONE_NAME="ventd-smoke-${DISTRO}-$(date +%s)"
log "cloning template ${TEMPLATE_VMID} → VM ${VMID} (${CLONE_NAME})"
PVE "qm clone $TEMPLATE_VMID $VMID --name $CLONE_NAME" >"$RESULT_DIR/clone.log" 2>&1

log "attaching cloud-init snippet"
PVE "qm set $VMID --cicustom 'user=${PVE_STORAGE}:snippets/${REMOTE_SNIPPET_NAME}' --ipconfig0 ip=dhcp" >/dev/null

log "starting VM $VMID"
PVE "qm start $VMID" >/dev/null

# ── Wait for guest agent, then for cloud-init DONE marker ───────────────────

log "waiting for qemu-guest-agent (up to ${GUEST_TIMEOUT}s)"
agent_ready=0
deadline=$(( $(date +%s) + GUEST_TIMEOUT ))
while (( $(date +%s) < deadline )); do
    if PVE "qm guest cmd $VMID ping" >/dev/null 2>&1; then
        agent_ready=1
        break
    fi
    sleep 5
done
(( agent_ready == 1 )) || die "guest agent never responded within ${GUEST_TIMEOUT}s"
log "guest agent up"

log "waiting for cloud-init DONE marker"
done_ready=0
# cloud-init runs packages + downloads + install, which can take another 2-3 min
# on a first-boot image. Reuse the same budget window from "now".
deadline=$(( $(date +%s) + GUEST_TIMEOUT ))
while (( $(date +%s) < deadline )); do
    if PVE "qm guest exec $VMID -- test -f /var/log/ventd-smoke/DONE" \
        2>/dev/null | grep -q '"exitcode" : 0'; then
        done_ready=1
        break
    fi
    sleep 5
done

# ── Pull result JSON + install log ──────────────────────────────────────────

# Proxmox's guest-exec has a 65k stdout cap per call; read the small JSON
# file first, the install log second (which can be large but we only care
# about the tail).
fetch_guest_file() {
    local path="$1"
    PVE "qm guest exec $VMID -- cat $path" 2>/dev/null |
        python3 -c 'import json,sys; j=json.load(sys.stdin); sys.stdout.write(j.get("out-data",""))'
}

if (( done_ready == 0 )); then
    log "WARNING: DONE marker missing — collecting partial state"
fi

fetch_guest_file "/var/log/ventd-smoke/install.log" > "$RESULT_DIR/install.log" 2>/dev/null || true
fetch_guest_file "$RESULT_PATH_IN_GUEST"            > "$RESULT_DIR/result.json" 2>/dev/null || true

if [[ ! -s "$RESULT_DIR/result.json" ]]; then
    # One more chance: run the collector live (in case DONE didn't fire).
    log "re-running collector in-guest"
    PVE "qm guest exec $VMID -- /usr/local/sbin/ventd-smoke-collect.sh $RESULT_PATH_IN_GUEST" >/dev/null 2>&1 || true
    fetch_guest_file "$RESULT_PATH_IN_GUEST" > "$RESULT_DIR/result.json" 2>/dev/null || true
fi

[[ -s "$RESULT_DIR/result.json" ]] || die "could not retrieve result JSON from guest"

# ── Assertions ──────────────────────────────────────────────────────────────

python3 - "$RESULT_DIR/result.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1]))
checks = [
    ("install_rc == 0",       r.get("install_rc") == 0),
    ("systemctl is-active",   r.get("systemctl_active") == "active"),
    ("listening on :9999",    int(r.get("listen_9999_count", 0)) >= 1),
    ("/api/ping reachable",   r.get("api_ping_https") == "200" or r.get("api_ping_http") == "200"),
    ("setup token in journal", int(r.get("setup_token_logged", 0)) == 1),
    ("/etc/ventd exists",     int(r.get("config_dir_exists", 0)) == 1),
    ("ventd proc user",       r.get("ventd_proc_user") == "ventd"),
]
print("="*60)
print(f"distro:  {r.get('os')}")
print(f"kernel:  {r.get('kernel')}")
print("-"*60)
fail = 0
for name, ok in checks:
    status = "PASS" if ok else "FAIL"
    if not ok: fail += 1
    print(f"  [{status}] {name}")
print("-"*60)
print("RESULT JSON:")
print(json.dumps(r, indent=2))
print("="*60)
sys.exit(0 if fail == 0 else 1)
PY

ASSERTIONS_PASSED=1
log "smoke PASSED for $DISTRO"
