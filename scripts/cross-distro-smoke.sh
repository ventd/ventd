#!/usr/bin/env bash
# scripts/cross-distro-smoke.sh — Proxmox-driven cross-distro install smoke
#
# For each distro template listed in the config, the harness:
#
#   1. Clones the template into a disposable VM (via the Proxmox REST API).
#   2. Starts the clone and waits for the QEMU guest agent to report an IP.
#   3. SSHes into the clone, runs `scripts/install.sh` from the release tag
#      under test, waits for the service to become active, and probes
#      /api/ping.
#   4. Destroys the clone on every exit path (success, failure, Ctrl-C).
#
# Writes two artefacts:
#
#   - docs/cross-distro-runs/YYYY-MM-DD-<ventd-version>.md  (this run)
#   - docs/cross-distro-status.md                           (append-only
#                                                            footer; the
#                                                            main table is
#                                                            human-owned)
#
# Usage:
#   ./scripts/cross-distro-smoke.sh                  # all distros in config
#   ./scripts/cross-distro-smoke.sh ubuntu-24-04     # just one
#
# Environment overrides:
#   PROXMOX_DRY_RUN=1   mock every HTTP call + every SSH command and write
#                       a dummy run log. Exercises the script's plumbing
#                       without touching Proxmox or any VM. Used in CI /
#                       CC terminals that don't have API access.
#   CONFIG_FILE         path to the config (default: scripts/cross-distro-smoke.config.sh)

set -euo pipefail

# ── Sanity checks ──────────────────────────────────────────────────────────

if (( BASH_VERSINFO[0] < 4 )); then
    echo "error: bash 4+ required (associative arrays). Found: $BASH_VERSION" >&2
    exit 2
fi

for dep in curl jq ssh date awk; do
    command -v "$dep" >/dev/null 2>&1 || {
        echo "error: missing dependency: $dep" >&2
        exit 2
    }
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

PROXMOX_DRY_RUN="${PROXMOX_DRY_RUN:-0}"
CONFIG_FILE="${CONFIG_FILE:-$SCRIPT_DIR/cross-distro-smoke.config.sh}"

# ── Load config ────────────────────────────────────────────────────────────

if [[ ! -f "$CONFIG_FILE" ]]; then
    if (( PROXMOX_DRY_RUN == 1 )); then
        # In dry-run we synthesise a minimal config so plumbing can still run.
        CONFIG_FILE="$SCRIPT_DIR/cross-distro-smoke.config.sh.example"
        echo "[dry-run] no config at default path; loading example: $CONFIG_FILE" >&2
    else
        echo "error: config not found: $CONFIG_FILE" >&2
        echo "       copy scripts/cross-distro-smoke.config.sh.example and edit" >&2
        exit 2
    fi
fi

# shellcheck source=cross-distro-smoke.config.sh.example
# shellcheck disable=SC1091
source "$CONFIG_FILE"

# Declared in the config via `declare -A TEMPLATE_VMIDS=(...)` — shellcheck
# can't follow sourced files, silence SC2154.
# shellcheck disable=SC2154
: "${TEMPLATE_VMIDS[@]:?TEMPLATE_VMIDS must be declared in $CONFIG_FILE}"

: "${PROXMOX_HOST:?PROXMOX_HOST must be set in $CONFIG_FILE}"
: "${PROXMOX_NODE:?PROXMOX_NODE must be set in $CONFIG_FILE}"
: "${PROXMOX_TOKEN_ID:?PROXMOX_TOKEN_ID must be set in $CONFIG_FILE}"
: "${PROXMOX_TOKEN_SECRET:?PROXMOX_TOKEN_SECRET must be set in $CONFIG_FILE}"
: "${SSH_KEY_PATH:?SSH_KEY_PATH must be set in $CONFIG_FILE}"
: "${SSH_USER:?SSH_USER must be set in $CONFIG_FILE}"
: "${VENTD_VERSION:?VENTD_VERSION must be set in $CONFIG_FILE}"

PROXMOX_PORT="${PROXMOX_PORT:-8006}"
PROXMOX_STORAGE="${PROXMOX_STORAGE:-local-lvm}"
PROXMOX_FULL_CLONE="${PROXMOX_FULL_CLONE:-1}"
CLONE_VMID_MIN="${CLONE_VMID_MIN:-9500}"
CLONE_VMID_MAX="${CLONE_VMID_MAX:-9599}"
AGENT_IP_TIMEOUT="${AGENT_IP_TIMEOUT:-120}"
VENTD_START_TIMEOUT="${VENTD_START_TIMEOUT:-60}"
TASK_POLL_TIMEOUT="${TASK_POLL_TIMEOUT:-600}"
SSH_CONNECT_TIMEOUT="${SSH_CONNECT_TIMEOUT:-30}"

API_BASE="https://${PROXMOX_HOST}:${PROXMOX_PORT}/api2/json"

# ── Logging helpers ────────────────────────────────────────────────────────

log()  { printf '[%s] %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }
warn() { printf '[%s] WARN: %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }
die()  { printf '[%s] FATAL: %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; exit 1; }

# ── HTTP layer ─────────────────────────────────────────────────────────────
#
# pve_curl wraps curl with the PVE API token auth header and the --insecure
# flag (Proxmox self-signed certs are the default and these VMs are
# throwaway).
#
# In dry-run mode, pve_curl returns canned JSON instead of hitting the
# network. The canned responses cover exactly the endpoints the harness
# calls: /cluster/nextid, /qemu/<id>/clone, /qemu/<id>/status/start,
# /qemu/<id>/agent/network-get-interfaces, /qemu/<id> (DELETE), and the
# task status poll /tasks/<upid>/status.

pve_curl_real() {
    curl \
        --silent --show-error --fail --insecure \
        --header "Authorization: PVEAPIToken=${PROXMOX_TOKEN_ID}=${PROXMOX_TOKEN_SECRET}" \
        "$@"
}

pve_curl_mock() {
    # Parse out the method and path from the varargs. We only look at the
    # last positional (the URL) and any -X method flag.
    local method="GET" url=""
    local args=("$@") i=0
    while (( i < ${#args[@]} )); do
        case "${args[$i]}" in
            -X)               method="${args[$((i+1))]}"; i=$((i+2)) ;;
            --request)        method="${args[$((i+1))]}"; i=$((i+2)) ;;
            -X*)              method="${args[$i]#-X}"; i=$((i+1)) ;;
            --data|--data-urlencode|-d)
                              i=$((i+2)) ;;
            -*)               i=$((i+1)) ;;
            *)                url="${args[$i]}"; i=$((i+1)) ;;
        esac
    done

    local path="${url#"$API_BASE"}"
    case "$method:$path" in
        GET:/cluster/nextid)
            echo '{"data":"9501"}' ;;
        POST:/nodes/*/qemu/*/clone)
            echo '{"data":"UPID:pve:0000DEAD:0000BEEF:5F000000:qmclone:9501:root@pam:"}' ;;
        POST:/nodes/*/qemu/*/status/start)
            echo '{"data":"UPID:pve:0000DEAD:0000BEEF:5F000001:qmstart:9501:root@pam:"}' ;;
        GET:/nodes/*/qemu/*/agent/network-get-interfaces)
            cat <<'JSON'
{
  "data": {
    "result": [
      {"name": "lo",   "ip-addresses": [{"ip-address": "127.0.0.1",      "ip-address-type": "ipv4"}]},
      {"name": "eth0", "ip-addresses": [{"ip-address": "fe80::1",        "ip-address-type": "ipv6"},
                                         {"ip-address": "10.88.0.42",    "ip-address-type": "ipv4"}]}
    ]
  }
}
JSON
            ;;
        DELETE:/nodes/*/qemu/*)
            echo '{"data":"UPID:pve:0000DEAD:0000BEEF:5F000002:qmdestroy:9501:root@pam:"}' ;;
        GET:/nodes/*/tasks/*/status)
            echo '{"data":{"status":"stopped","exitstatus":"OK"}}' ;;
        *)
            echo "mock: unknown endpoint $method $path" >&2
            return 22
            ;;
    esac
}

pve_curl() {
    if (( PROXMOX_DRY_RUN == 1 )); then
        pve_curl_mock "$@"
    else
        pve_curl_real "$@"
    fi
}

# Extract .data from the PVE response envelope. On error (.errors present),
# dump to stderr and exit non-zero.
pve_data() {
    local body="$1"
    if echo "$body" | jq -e '.errors' >/dev/null 2>&1; then
        echo "PVE API error: $body" >&2
        return 1
    fi
    echo "$body" | jq -r '.data'
}

# url-encode a PVE UPID so it can safely appear as a path segment.
# UPIDs contain ':' which is legal per RFC 3986 but some proxies still
# trip on it. Also handles '@'.
urlencode_upid() {
    local s="$1"
    s="${s//:/%3A}"
    s="${s//@/%40}"
    echo "$s"
}

# Poll a UPID task until it exits. Returns 0 on OK, 1 on non-OK or timeout.
wait_task() {
    local upid="$1" deadline=$(( $(date +%s) + TASK_POLL_TIMEOUT ))
    local encoded status body rc
    encoded="$(urlencode_upid "$upid")"
    while (( $(date +%s) < deadline )); do
        body="$(pve_curl "$API_BASE/nodes/$PROXMOX_NODE/tasks/$encoded/status")" \
            || { sleep 3; continue; }
        status="$(echo "$body" | jq -r '.data.status // empty')"
        if [[ "$status" == "stopped" ]]; then
            rc="$(echo "$body" | jq -r '.data.exitstatus // empty')"
            if [[ "$rc" == "OK" ]]; then
                return 0
            fi
            warn "task $upid finished with exitstatus=$rc"
            return 1
        fi
        sleep 3
    done
    warn "task $upid did not finish in ${TASK_POLL_TIMEOUT}s"
    return 1
}

# ── VM lifecycle helpers ───────────────────────────────────────────────────

# Pick a free VMID in the configured range. Strategy: try nextid first; if
# it lands outside the range or is already taken, scan the range until a
# free one is found.
allocate_vmid() {
    local suggested vmid body
    body="$(pve_curl "$API_BASE/cluster/nextid")" || return 1
    suggested="$(pve_data "$body")"
    if (( suggested >= CLONE_VMID_MIN && suggested <= CLONE_VMID_MAX )); then
        echo "$suggested"
        return 0
    fi
    for (( vmid = CLONE_VMID_MIN; vmid <= CLONE_VMID_MAX; vmid++ )); do
        # /cluster/nextid?vmid=N returns the VMID if free, or errors out.
        if pve_curl "$API_BASE/cluster/nextid?vmid=$vmid" >/dev/null 2>&1; then
            echo "$vmid"
            return 0
        fi
    done
    warn "no free VMID in [$CLONE_VMID_MIN, $CLONE_VMID_MAX]"
    return 1
}

clone_template() {
    local template_vmid="$1" new_vmid="$2" name="$3" upid body
    local form=(
        --data-urlencode "newid=$new_vmid"
        --data-urlencode "name=$name"
        --data-urlencode "full=$PROXMOX_FULL_CLONE"
    )
    if [[ -n "${PROXMOX_STORAGE:-}" && "$PROXMOX_FULL_CLONE" == "1" ]]; then
        form+=(--data-urlencode "storage=$PROXMOX_STORAGE")
    fi
    body="$(pve_curl -X POST "${form[@]}" \
        "$API_BASE/nodes/$PROXMOX_NODE/qemu/$template_vmid/clone")" || return 1
    upid="$(pve_data "$body")"
    [[ -n "$upid" && "$upid" != "null" ]] || { warn "clone returned empty UPID"; return 1; }
    wait_task "$upid"
}

start_vm() {
    local vmid="$1" upid body
    body="$(pve_curl -X POST "$API_BASE/nodes/$PROXMOX_NODE/qemu/$vmid/status/start")" \
        || return 1
    upid="$(pve_data "$body")"
    [[ -n "$upid" && "$upid" != "null" ]] || { warn "start returned empty UPID"; return 1; }
    wait_task "$upid"
}

destroy_vm() {
    local vmid="$1" upid body
    body="$(pve_curl -X DELETE \
        "$API_BASE/nodes/$PROXMOX_NODE/qemu/$vmid?purge=1&destroy-unreferenced-disks=1")" \
        || return 1
    upid="$(pve_data "$body")"
    [[ -n "$upid" && "$upid" != "null" ]] || { warn "destroy returned empty UPID"; return 1; }
    wait_task "$upid"
}

# Query the guest agent for interface IPs. Returns the first globally-routable
# IPv4 address on a non-loopback, non-docker, non-link-local interface.
guest_ipv4() {
    local vmid="$1" body ip
    body="$(pve_curl "$API_BASE/nodes/$PROXMOX_NODE/qemu/$vmid/agent/network-get-interfaces")" \
        || return 1
    # Filter logic:
    #   - skip interface lo
    #   - skip interfaces with docker0/br- prefix
    #   - only ipv4 addresses
    #   - skip 127.*, 169.254.* (link-local), 172.17.* (default docker bridge)
    ip="$(echo "$body" | jq -r '
        .data.result[]
        | select(.name != "lo")
        | select((.name | startswith("docker")) | not)
        | select((.name | startswith("br-")) | not)
        | select((.name | startswith("veth")) | not)
        | ."ip-addresses"[]?
        | select(."ip-address-type" == "ipv4")
        | .["ip-address"]
        | select(startswith("127.") | not)
        | select(startswith("169.254.") | not)
        | select(startswith("172.17.") | not)
    ' | head -n1)"
    [[ -n "$ip" ]] || return 1
    echo "$ip"
}

wait_for_ip() {
    local vmid="$1" deadline=$(( $(date +%s) + AGENT_IP_TIMEOUT ))
    local ip
    while (( $(date +%s) < deadline )); do
        if ip="$(guest_ipv4 "$vmid" 2>/dev/null)" && [[ -n "$ip" ]]; then
            echo "$ip"
            return 0
        fi
        sleep 5
    done
    return 1
}

# ── SSH layer ──────────────────────────────────────────────────────────────

SSH_OPTS=(
    -i "$SSH_KEY_PATH"
    -o "BatchMode=yes"
    -o "ConnectTimeout=$SSH_CONNECT_TIMEOUT"
    -o "StrictHostKeyChecking=accept-new"
    -o "UserKnownHostsFile=/dev/null"
    -o "LogLevel=ERROR"
)

ssh_vm_real() {
    local host="$1"; shift
    # SC2029: expansion on the client is intentional — callers pass the
    # fully-formed command string (including VENTD_VERSION) and we want
    # ssh to deliver it verbatim.
    # shellcheck disable=SC2029
    ssh "${SSH_OPTS[@]}" "${SSH_USER}@${host}" "$@"
}

ssh_vm_mock() {
    local host="$1"; shift
    local cmd="$*"
    case "$cmd" in
        *"install.sh"*)
            echo "[mock] ventd install.sh exit 0 on $host"
            return 0 ;;
        *"systemctl is-active ventd"*)
            echo "active"
            return 0 ;;
        *"curl"*"/api/ping"*)
            echo "HTTP/1.1 200 OK"
            return 0 ;;
        *"journalctl"*)
            echo "[mock] journalctl tail unavailable in dry-run"
            return 0 ;;
        *)
            echo "[mock] ssh $host: $cmd"
            return 0 ;;
    esac
}

ssh_vm() {
    if (( PROXMOX_DRY_RUN == 1 )); then
        ssh_vm_mock "$@"
    else
        ssh_vm_real "$@"
    fi
}

# ── Smoke test in-guest ────────────────────────────────────────────────────
#
# Installs the pinned ventd release, waits for the service to come up, then
# hits /api/ping. On failure, captures the last 100 lines of the journal
# into $3 (a file on the dev host). Returns:
#   0  pass
#   1  install.sh failed
#   2  service never became active
#   3  /api/ping non-200

smoke_vm() {
    local vmip="$1" journal_out="$2"
    local install_cmd systemd_check ping_check

    install_cmd="curl -fsSL https://raw.githubusercontent.com/ventd/ventd/${VENTD_VERSION}/scripts/install.sh"
    install_cmd+=" | VENTD_VERSION=${VENTD_VERSION} sudo -E bash"

    if ! ssh_vm "$vmip" "$install_cmd"; then
        ssh_vm "$vmip" "journalctl -u ventd --no-pager -n 100 || true" \
            > "$journal_out" 2>&1 || true
        return 1
    fi

    # Poll systemctl is-active up to VENTD_START_TIMEOUT.
    local deadline=$(( $(date +%s) + VENTD_START_TIMEOUT ))
    local active=""
    while (( $(date +%s) < deadline )); do
        active="$(ssh_vm "$vmip" "systemctl is-active ventd 2>/dev/null || true")"
        [[ "$active" == "active" ]] && break
        sleep 3
    done
    if [[ "$active" != "active" ]]; then
        ssh_vm "$vmip" "journalctl -u ventd --no-pager -n 100 || true" \
            > "$journal_out" 2>&1 || true
        return 2
    fi

    # /api/ping from the dev host (the LAN-reachable path — matches the
    # zero-terminal promise in .claude/rules/usability.md).
    if (( PROXMOX_DRY_RUN == 1 )); then
        ping_check="HTTP/1.1 200 OK"
    else
        ping_check="$(curl -fsSL -o /dev/null -w '%{http_code}\n' \
            --max-time 10 \
            "http://${vmip}:9999/api/ping" 2>/dev/null || true)"
    fi
    if [[ "$ping_check" != *"200"* ]]; then
        ssh_vm "$vmip" "journalctl -u ventd --no-pager -n 100 || true" \
            > "$journal_out" 2>&1 || true
        return 3
    fi

    systemd_check="$active"
    echo "install.sh=OK is-active=$systemd_check ping=200"
    return 0
}

# ── Report writer ──────────────────────────────────────────────────────────

RUN_DATE="$(date -u +%Y-%m-%d)"
RUN_TIMESTAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
RUNS_DIR="$REPO_ROOT/docs/cross-distro-runs"
RUN_FILE="$RUNS_DIR/${RUN_DATE}-${VENTD_VERSION}.md"
STATUS_FILE="$REPO_ROOT/docs/cross-distro-status.md"

mkdir -p "$RUNS_DIR"

run_header_written=0
init_run_file() {
    (( run_header_written == 1 )) && return 0
    run_header_written=1
    {
        echo "# Cross-distro smoke — ${RUN_DATE} — ventd ${VENTD_VERSION}"
        echo ""
        echo "- Run started: \`${RUN_TIMESTAMP}\`"
        echo "- Proxmox node: \`${PROXMOX_NODE}\` on \`${PROXMOX_HOST}\`"
        echo "- Dry-run: \`${PROXMOX_DRY_RUN}\`"
        echo ""
        echo "| Distro | Result | VMID | IP | Notes |"
        echo "|--------|--------|-----:|----|-------|"
    } > "$RUN_FILE"
}

record_result() {
    local distro="$1" result="$2" vmid="$3" ip="$4" notes="$5"
    init_run_file
    printf '| %s | %s | %s | %s | %s |\n' \
        "$distro" "$result" "${vmid:--}" "${ip:--}" "${notes:--}" >> "$RUN_FILE"
}

append_status_footer() {
    local pass_count="$1" fail_count="$2" skip_count="$3"
    local header="## Run footer — ${RUN_DATE} — ventd ${VENTD_VERSION}"
    {
        echo ""
        echo "---"
        echo ""
        echo "$header"
        echo ""
        echo "- Pass: ${pass_count}"
        echo "- Fail: ${fail_count}"
        echo "- Skip: ${skip_count}"
        echo "- Detail: [docs/cross-distro-runs/${RUN_DATE}-${VENTD_VERSION}.md](cross-distro-runs/${RUN_DATE}-${VENTD_VERSION}.md)"
        echo ""
        echo "_Phoenix: review detail, then promote per-distro cells in the table above._"
    } >> "$STATUS_FILE"
}

# ── Per-distro driver ──────────────────────────────────────────────────────

CLEANUP_VMIDS=()
# shellcheck disable=SC2317  # invoked via `trap`, not directly.
cleanup_all() {
    local rc=$?
    local vmid
    for vmid in "${CLEANUP_VMIDS[@]}"; do
        log "cleanup: destroying VMID $vmid"
        destroy_vm "$vmid" || warn "cleanup destroy for $vmid failed"
    done
    exit "$rc"
}
trap cleanup_all EXIT INT TERM

run_distro() {
    local distro="$1"
    local template_vmid="${TEMPLATE_VMIDS[$distro]:-}"
    if [[ -z "$template_vmid" ]]; then
        warn "no template mapping for '$distro' — skipping"
        record_result "$distro" "SKIP" "" "" "no TEMPLATE_VMIDS entry"
        return 2
    fi

    log "=== $distro (template VMID $template_vmid) ==="

    local new_vmid
    new_vmid="$(allocate_vmid)" || {
        record_result "$distro" "FAIL" "" "" "could not allocate VMID"
        return 1
    }
    CLEANUP_VMIDS+=("$new_vmid")
    log "allocated VMID $new_vmid"

    local clone_name="ventd-smoke-${distro}-${new_vmid}"
    if ! clone_template "$template_vmid" "$new_vmid" "$clone_name"; then
        record_result "$distro" "FAIL" "$new_vmid" "" "clone task failed"
        return 1
    fi
    log "clone $new_vmid ready"

    if ! start_vm "$new_vmid"; then
        record_result "$distro" "FAIL" "$new_vmid" "" "start task failed"
        return 1
    fi
    log "start $new_vmid issued"

    local vmip=""
    if ! vmip="$(wait_for_ip "$new_vmid")"; then
        record_result "$distro" "FAIL" "$new_vmid" "" "guest agent never reported IP (${AGENT_IP_TIMEOUT}s)"
        return 1
    fi
    log "guest agent reports IP $vmip"

    local journal_file="$RUNS_DIR/.${RUN_DATE}-${distro}-journal.log"
    local smoke_rc=0
    smoke_vm "$vmip" "$journal_file" || smoke_rc=$?

    case "$smoke_rc" in
        0)
            record_result "$distro" "PASS" "$new_vmid" "$vmip" "install.sh + is-active + /api/ping"
            ;;
        1)
            record_result "$distro" "FAIL" "$new_vmid" "$vmip" "install.sh non-zero (journal tail inline below)"
            append_journal_tail "$distro" "$journal_file"
            ;;
        2)
            record_result "$distro" "FAIL" "$new_vmid" "$vmip" "ventd never became active (${VENTD_START_TIMEOUT}s)"
            append_journal_tail "$distro" "$journal_file"
            ;;
        3)
            record_result "$distro" "FAIL" "$new_vmid" "$vmip" "/api/ping non-200"
            append_journal_tail "$distro" "$journal_file"
            ;;
    esac

    rm -f "$journal_file"
    return "$smoke_rc"
}

append_journal_tail() {
    local distro="$1" file="$2"
    [[ -s "$file" ]] || return 0
    {
        echo ""
        echo "### journal tail — $distro"
        echo ""
        echo '```'
        head -c 16000 "$file"
        echo '```'
    } >> "$RUN_FILE"
}

# ── Argument parsing ───────────────────────────────────────────────────────

TARGET_DISTROS=()
if (( $# == 0 )); then
    # All configured distros, in a stable order so the output matches the
    # status matrix row ordering.
    for d in ubuntu-24-04 debian-12 fedora-40 arch opensuse-tumbleweed void-glibc alpine-3-19; do
        if [[ -n "${TEMPLATE_VMIDS[$d]:-}" ]]; then
            TARGET_DISTROS+=("$d")
        fi
    done
    # Anything else the user put in TEMPLATE_VMIDS (custom slugs) — append
    # in map-iteration order (unstable but deterministic within a bash run).
    for d in "${!TEMPLATE_VMIDS[@]}"; do
        case " ${TARGET_DISTROS[*]} " in
            *" $d "*) continue ;;
            *) TARGET_DISTROS+=("$d") ;;
        esac
    done
else
    TARGET_DISTROS=("$@")
fi

if (( ${#TARGET_DISTROS[@]} == 0 )); then
    die "no distros to run — check TEMPLATE_VMIDS in $CONFIG_FILE"
fi

# ── Main loop ──────────────────────────────────────────────────────────────

log "ventd cross-distro smoke — ${VENTD_VERSION} — ${#TARGET_DISTROS[@]} distro(s)"
log "dry-run: $PROXMOX_DRY_RUN"

pass=0; fail=0; skip=0
for distro in "${TARGET_DISTROS[@]}"; do
    rc=0
    run_distro "$distro" || rc=$?
    # Pre-increment, not post — under `set -e`, `(( x++ ))` returns 0
    # when x was 0, which bash treats as a failed arithmetic command
    # and exits the script.
    case "$rc" in
        0) (( ++pass )) ;;
        2) (( ++skip )) ;;
        *) (( ++fail )) ;;
    esac
    # Once a distro has been dealt with, drop its VMID from CLEANUP_VMIDS
    # since destroy_vm already ran. The cleanup trap only exists to catch
    # Ctrl-C mid-flight; double-destroying is just noise.
    if (( ${#CLEANUP_VMIDS[@]} > 0 )); then
        last="${CLEANUP_VMIDS[-1]}"
        unset 'CLEANUP_VMIDS[-1]'
        # `last` is allocated per-distro; we successfully destroyed it at
        # the end of run_distro (or it failed before clone, in which case
        # there's nothing to clean up anyway).
        destroy_vm "$last" >/dev/null 2>&1 || true
    fi
done

# ── Finalise report ────────────────────────────────────────────────────────

{
    echo ""
    echo "## Summary"
    echo ""
    echo "- Pass: $pass"
    echo "- Fail: $fail"
    echo "- Skip: $skip"
    echo ""
    echo "Run finished: \`$(date -u +%Y-%m-%dT%H:%M:%SZ)\`"
} >> "$RUN_FILE"

append_status_footer "$pass" "$fail" "$skip"

log "run file: $RUN_FILE"
log "status footer appended to: $STATUS_FILE"
log "pass=$pass fail=$fail skip=$skip"

trap - EXIT INT TERM
exit $(( fail > 0 ? 1 : 0 ))
