#!/usr/bin/env bash
# smoke.sh — Incus smoke test for ops-mcp.
#
# Runs on the host (phoenix-desktop or any Linux box with Incus).
# Creates a disposable Ubuntu 24.04 container, installs ops-mcp inside it,
# exercises all tools + OAuth endpoints, checks the audit log, and verifies
# allowlist rejection. Destroys the container on exit.
#
# Usage:
#   bash infra/ops-mcp/smoke.sh
#
# Exit code: 0 = all assertions passed, 1 = at least one failure.

set -uo pipefail
# NOTE: intentionally NOT using set -e so we can capture and print diagnostics
# on container-side failures. Inner bash -c blocks use explicit exit codes.

OPS_MCP_SRC="${OPS_MCP_SRC:-$(cd "$(dirname "$0")" && pwd)}"
CONTAINER="ops-mcp-smoke-$$"
PASS=0
FAIL=0

_pass() { echo "  pass: $*"; ((PASS++)) || true; }
_fail() { echo "  FAIL: $*"; ((FAIL++)) || true; }
_section() { echo; echo "=== $* ==="; }

# shellcheck disable=SC2317  # called via trap EXIT — not unreachable
cleanup() {
  echo
  echo "Destroying container $CONTAINER ..."
  # shellcheck disable=SC2317
  incus delete --force "$CONTAINER" 2>/dev/null || true
}
trap cleanup EXIT

_die() {
  echo
  echo "SMOKE: FAIL"
  echo "Reason: $*"
  exit 1
}

# ---------------------------------------------------------------------------
_section "Launching Incus container: $CONTAINER"
incus launch images:ubuntu/24.04 "$CONTAINER" || _die "incus launch failed"
sleep 3
incus exec "$CONTAINER" -- bash -c 'until ping -c1 8.8.8.8 &>/dev/null; do sleep 1; done' || _die "container not networked"
echo "  container up and networked"

# ---------------------------------------------------------------------------
_section "Installing dependencies"
incus exec "$CONTAINER" -- bash -c '
  set -e
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq --no-install-recommends \
    python3 python3-venv python3-pip \
    sudo systemd util-linux iproute2 curl ca-certificates
' || _die "apt-get install failed"

# ---------------------------------------------------------------------------
_section "Verifying tool availability"
for tool in python3 runuser sudo visudo ss curl; do
  if incus exec "$CONTAINER" -- bash -c "command -v $tool >/dev/null"; then
    _pass "$tool present"
  else
    _fail "$tool MISSING — apt-get did not install it"
    echo "--- apt log (last 40 lines) ---"
    incus exec "$CONTAINER" -- bash -c 'tail -40 /var/log/apt/term.log 2>/dev/null || echo no_apt_log' || true
    _die "required tool missing"
  fi
done

# ---------------------------------------------------------------------------
_section "Creating ops-mcp system user"
incus exec "$CONTAINER" -- useradd --system --no-create-home --shell /usr/sbin/nologin ops-mcp || _die "useradd failed"
if incus exec "$CONTAINER" -- id ops-mcp; then
  _pass "ops-mcp user created"
else
  _die "ops-mcp user missing after useradd"
fi

# ---------------------------------------------------------------------------
_section "Copying source into container"
TMPTAR=$(mktemp /tmp/ops-mcp-smoke-XXXXXX.tar.gz)
tar czf "$TMPTAR" -C "$(dirname "$OPS_MCP_SRC")" "$(basename "$OPS_MCP_SRC")"
incus file push "$TMPTAR" "$CONTAINER/tmp/ops-mcp-src.tar.gz" || _die "file push failed"
rm -f "$TMPTAR"
incus exec "$CONTAINER" -- bash -c '
  set -e
  mkdir -p /opt/ops-mcp
  tar xzf /tmp/ops-mcp-src.tar.gz -C /opt/ops-mcp --strip-components=1
  chown -R ops-mcp:ops-mcp /opt/ops-mcp
  ls -la /opt/ops-mcp/server.py >/dev/null || { echo "server.py missing after extract"; exit 1; }
' || _die "source extraction failed"
_pass "source tree extracted"

# ---------------------------------------------------------------------------
_section "Creating directories and env file"
incus exec "$CONTAINER" -- bash -c '
  set -e
  install -d -o ops-mcp -g ops-mcp -m 755 /var/log/ops-mcp /var/lib/ops-mcp
  cat > /etc/ops-mcp-env <<EOF
OPS_MCP_LOG=DEBUG
OPS_MCP_AUDIT=/var/log/ops-mcp/audit.jsonl
OPS_MCP_HOST=127.0.0.1
OPS_MCP_PORT=8892
EOF
  chmod 640 /etc/ops-mcp-env
  chown root:ops-mcp /etc/ops-mcp-env
  test -d /var/log/ops-mcp || { echo "/var/log/ops-mcp missing"; exit 1; }
' || _die "dirs/env file creation failed"
_pass "dirs + env file present"

# ---------------------------------------------------------------------------
_section "Installing Python venv and mcp"
incus exec "$CONTAINER" -- bash -c '
  set -e
  python3 -m venv /opt/ops-mcp/venv
  /opt/ops-mcp/venv/bin/pip install --quiet "mcp>=1.3.0"
  test -x /opt/ops-mcp/venv/bin/python || { echo "venv python missing"; exit 1; }
' || _die "venv install failed"
_pass "venv + mcp installed"

# ---------------------------------------------------------------------------
_section "Installing sudoers fragment"
incus file push "$OPS_MCP_SRC/ops-mcp.sudoers" "$CONTAINER/etc/sudoers.d/ops-mcp" || _die "sudoers file push failed"
incus exec "$CONTAINER" -- chmod 440 /etc/sudoers.d/ops-mcp
if incus exec "$CONTAINER" -- visudo -cf /etc/sudoers.d/ops-mcp; then
  _pass "sudoers fragment validates"
else
  _fail "sudoers fragment has syntax errors"
fi

# ---------------------------------------------------------------------------
_section "Starting ops-mcp server"
# CRITICAL: use --cwd /opt/ops-mcp so FastMCP's pydantic_settings can stat
# .env in a readable directory. Default CWD is /root (700 root:root) which
# ops-mcp cannot stat into → PermissionError: '.env'. Production systemd
# unit sets WorkingDirectory=/opt/ops-mcp which accomplishes the same.
# shellcheck disable=SC2016  # $(…) inside single-quoted bash are intentional for container context
incus exec "$CONTAINER" --cwd /opt/ops-mcp -- bash -c '
  set -a; source /etc/ops-mcp-env; set +a
  install -o ops-mcp -g ops-mcp -m 644 /dev/null /var/log/ops-mcp/server.log
  cd /opt/ops-mcp
  nohup runuser -u ops-mcp -- /opt/ops-mcp/venv/bin/python /opt/ops-mcp/server.py \
    >> /var/log/ops-mcp/server.log 2>&1 &
  echo $! > /tmp/ops-mcp.pid
  echo "  launched pid: $(cat /tmp/ops-mcp.pid)"
'
sleep 3

# Always-print diagnostics so failure modes are visible
echo "--- process check ---"
OPS_PID=$(incus exec "$CONTAINER" -- cat /tmp/ops-mcp.pid 2>/dev/null || echo "")
if [ -n "$OPS_PID" ] && incus exec "$CONTAINER" -- kill -0 "$OPS_PID" 2>/dev/null; then
  echo "  process alive (pid: $OPS_PID)"
else
  echo "  process DIED"
fi
echo "--- server.log (full) ---"
incus exec "$CONTAINER" -- cat /var/log/ops-mcp/server.log 2>&1 || echo "  (log unreadable)"
echo "--- listening sockets ---"
incus exec "$CONTAINER" -- ss -tlnp 2>&1 | head -20 || true
echo "--- ops-mcp file layout ---"
incus exec "$CONTAINER" -- ls -la /opt/ops-mcp/ 2>&1 || true
echo "--- Python version in venv ---"
incus exec "$CONTAINER" -- /opt/ops-mcp/venv/bin/python --version 2>&1 || true
echo "--- end diagnostics ---"

if incus exec "$CONTAINER" -- bash -c 'ss -tlnp 2>/dev/null | grep -q ":8892 "'; then
  _pass "server listening on 127.0.0.1:8892"
else
  _fail "server not listening on 8892 (see diagnostics above)"
  _die "server did not reach listening state"
fi

# ---------------------------------------------------------------------------
_section "A1: OAuth /.well-known/oauth-authorization-server"
OAUTH_META=$(incus exec "$CONTAINER" -- \
  curl -sf http://127.0.0.1:8892/.well-known/oauth-authorization-server)
if echo "$OAUTH_META" | python3 -c "
import json,sys
d=json.load(sys.stdin)
assert 'authorization_endpoint' in d, 'missing authorization_endpoint'
assert 'token_endpoint' in d, 'missing token_endpoint'
assert 'S256' in d.get('code_challenge_methods_supported',[]), 'missing S256'
"; then
  _pass "oauth-as-metadata has correct fields"
else
  _fail "oauth-as-metadata fields wrong: $OAUTH_META"
fi

_section "A2: OAuth /.well-known/oauth-protected-resource"
PR_META=$(incus exec "$CONTAINER" -- \
  curl -sf http://127.0.0.1:8892/.well-known/oauth-protected-resource)
if echo "$PR_META" | python3 -c "
import json,sys
d=json.load(sys.stdin)
assert 'authorization_servers' in d
"; then
  _pass "oauth-pr-metadata OK"
else
  _fail "oauth-pr-metadata wrong: $PR_META"
fi

_section "A3: OAuth /token"
TOKEN_RESP=$(incus exec "$CONTAINER" -- \
  curl -sf -X POST http://127.0.0.1:8892/token)
if echo "$TOKEN_RESP" | python3 -c "
import json,sys
d=json.load(sys.stdin)
assert 'access_token' in d
assert d.get('token_type') == 'Bearer'
"; then
  _pass "token endpoint returns Bearer token"
else
  _fail "token endpoint wrong: $TOKEN_RESP"
fi

# ---------------------------------------------------------------------------
_section "A4-A7: Tool calls via direct Python import"
incus exec "$CONTAINER" --cwd /opt/ops-mcp -- bash -c '
set -a; source /etc/ops-mcp-env; set +a
/opt/ops-mcp/venv/bin/python - <<'"'"'PYEOF'"'"'
import sys, os, json
sys.path.insert(0, "/opt/ops-mcp")
os.chdir("/opt/ops-mcp")
os.environ.update({
    "OPS_MCP_LOG": "DEBUG",
    "OPS_MCP_AUDIT": "/var/log/ops-mcp/audit.jsonl",
    "OPS_MCP_HOST": "127.0.0.1",
    "OPS_MCP_PORT": "8892",
})

from server import (
    _is_allowlisted, _parse_systemd_show,
    systemctl_status, systemctl_list_failed,
    journalctl, AUDIT_LOG
)

results = []

for svc in ["spawn-mcp", "ops-mcp", "cloudflared",
            "actions.runner.ventd-ventd.runner1.service"]:
    if _is_allowlisted(svc):
        results.append(("pass", "allowlist: %s accepted" % svc))
    else:
        results.append(("FAIL", "allowlist: %s should be accepted" % svc))

for svc in ["sshd", "nginx", "actions.runner.other.runner.service"]:
    if not _is_allowlisted(svc):
        results.append(("pass", "allowlist rejection: %s correctly blocked" % svc))
    else:
        results.append(("FAIL", "allowlist: %s should be blocked" % svc))

try:
    r = systemctl_status("ops-mcp")
    assert "active" in r, "missing active key: %s" % r
    assert "substate" in r
    assert "main_pid" in r
    assert "started_at" in r
    assert "restart_count" in r
    results.append(("pass", "systemctl_status returned structured dict: active=%s" % r["active"]))
except Exception as e:
    results.append(("FAIL", "systemctl_status raised: %s" % e))

try:
    r = systemctl_list_failed()
    assert "services" in r, "missing services key: %s" % r
    results.append(("pass", "systemctl_list_failed returned list (len=%d)" % len(r["services"])))
except Exception as e:
    results.append(("FAIL", "systemctl_list_failed raised: %s" % e))

try:
    r = journalctl("ops-mcp", lines=10)
    assert "logs" in r
    assert "truncated" in r
    results.append(("pass", "journalctl returned logs dict"))
except Exception as e:
    results.append(("FAIL", "journalctl raised: %s" % e))

try:
    from server import systemctl_restart
    systemctl_restart("sshd")
    results.append(("FAIL", "systemctl_restart(sshd) should have raised ValueError"))
except ValueError as e:
    results.append(("pass", "systemctl_restart rejection: %s" % e))

try:
    with open(AUDIT_LOG) as f:
        lines = f.readlines()
    assert len(lines) > 0, "audit log is empty"
    last = json.loads(lines[-1])
    assert "ts" in last, "audit entry missing ts"
    results.append(("pass", "audit log has %d entries; last kind=%s" % (len(lines), last.get("kind"))))
except Exception as e:
    results.append(("FAIL", "audit log check: %s" % e))

fail_count = 0
for status, msg in results:
    print("  %s: %s" % (status, msg))
    if status == "FAIL":
        fail_count += 1
sys.exit(fail_count)
PYEOF
'

TOOL_RC=$?

# ---------------------------------------------------------------------------
_section "A8: New tools — fixture-based smoke via direct Python import"
NOW_ISO=$(date -u +%Y-%m-%dT%H:%M:%SZ)
incus exec "$CONTAINER" --cwd /opt/ops-mcp -- bash -c "
set -a; source /etc/ops-mcp-env; set +a

# Synthetic fixtures for filesystem-dependent tools
mkdir -p /var/log/spawn-mcp/sessions /tmp/spawn-mcp /tmp/smoke-fixtures

cat > /var/log/spawn-mcp/audit.jsonl <<EOF
{\"kind\": \"tool_call\", \"ts\": \"${NOW_ISO}\", \"tool\": \"journalctl\"}
{\"kind\": \"reject\",    \"ts\": \"${NOW_ISO}\", \"tool\": \"systemctl_restart\"}
EOF

cat > /var/log/spawn-mcp/sessions/test-session.log <<EOF
2024-01-01 00:00:00 ops-mcp started
spawn-mcp: claude exited rc=0
EOF

touch /tmp/spawn-mcp/old-prompt.txt
touch /tmp/spawn-mcp/new-prompt.txt
touch -t 202001010000 /tmp/spawn-mcp/old-prompt.txt
" || _die "fixture creation failed"

incus exec "$CONTAINER" --cwd /opt/ops-mcp -- bash -c '
set -a; source /etc/ops-mcp-env; set +a
export OPS_MCP_SPAWN_LOG_DIR=/var/log/spawn-mcp
export OPS_MCP_SPAWN_TMPFS_DIR=/tmp/spawn-mcp
export OPS_MCP_GHRUNNER_DIAG_DIR=/tmp/smoke-fixtures/ghrunner-diag
/opt/ops-mcp/venv/bin/python - <<'"'"'PYEOF'"'"'
import sys, os, json, asyncio, time
sys.path.insert(0, "/opt/ops-mcp")
os.chdir("/opt/ops-mcp")
os.environ.update({
    "OPS_MCP_LOG": "DEBUG",
    "OPS_MCP_AUDIT": "/var/log/ops-mcp/audit.jsonl",
    "OPS_MCP_HOST": "127.0.0.1",
    "OPS_MCP_PORT": "8892",
    "OPS_MCP_SPAWN_LOG_DIR": "/var/log/spawn-mcp",
    "OPS_MCP_SPAWN_TMPFS_DIR": "/tmp/spawn-mcp",
    "OPS_MCP_GHRUNNER_DIAG_DIR": "/tmp/smoke-fixtures/ghrunner-diag",
})
import server

results = []

# --- disk_free ---
try:
    r = server.disk_free("/")
    assert r["total_bytes"] > 0, "total_bytes zero: %s" % r
    assert "free_bytes" in r
    total = r["total_bytes"]
    free = r["free_bytes"]
    results.append(("pass", "disk_free: total=%d free=%d" % (total, free)))
except Exception as e:
    results.append(("FAIL", "disk_free: %s" % e))

# --- disk_free rejection ---
try:
    server.disk_free("/no/such/path")
    results.append(("FAIL", "disk_free: expected ValueError for missing path"))
except ValueError as e:
    results.append(("pass", "disk_free rejects bad path: %s" % e))

# --- hwmon_snapshot ---
try:
    r = server.hwmon_snapshot()
    assert "chips" in r, "missing chips key"
    n = len(r["chips"])
    results.append(("pass", "hwmon_snapshot: %d chips" % n))
except Exception as e:
    results.append(("FAIL", "hwmon_snapshot: %s" % e))

# --- cc_log_find (fixture) ---
try:
    r = server.cc_log_find("test-session")
    assert r["exit_code"] == 0, "wrong exit_code: %s" % r
    assert r["size_bytes"] > 0
    ec = r["exit_code"]
    results.append(("pass", "cc_log_find: exit_code=%s" % ec))
except Exception as e:
    results.append(("FAIL", "cc_log_find: %s" % e))

# --- cc_log_find path traversal rejection ---
try:
    server.cc_log_find("../etc/passwd")
    results.append(("FAIL", "cc_log_find: expected ValueError for traversal"))
except ValueError:
    results.append(("pass", "cc_log_find rejects path traversal"))

# --- cc_audit_log (fixture) ---
try:
    r = server.cc_audit_log("1 hour ago")
    assert "events" in r
    n = len(r["events"])
    assert n == 2, "expected 2 events, got %d" % n
    results.append(("pass", "cc_audit_log: %d events" % n))
except Exception as e:
    results.append(("FAIL", "cc_audit_log: %s" % e))

# --- cc_audit_log kind filter ---
try:
    r = server.cc_audit_log("1 hour ago", kind_filter=["reject"])
    n = len(r["events"])
    assert n == 1, "expected 1 reject event, got %d" % n
    results.append(("pass", "cc_audit_log kind_filter works"))
except Exception as e:
    results.append(("FAIL", "cc_audit_log kind_filter: %s" % e))

# --- tmpfs_clear_cc_prompts ---
try:
    r = server.tmpfs_clear_cc_prompts(age_min=60)
    assert "removed_files" in r
    assert "kept_files" in r
    nr = len(r["removed_files"])
    nk = len(r["kept_files"])
    results.append(("pass", "tmpfs_clear: removed=%d kept=%d" % (nr, nk)))
except Exception as e:
    results.append(("FAIL", "tmpfs_clear_cc_prompts: %s" % e))

# --- install_script_dry_run (no install.sh in container — expect error, not crash) ---
try:
    r = server.install_script_dry_run("ubuntu-24.04")
    assert "actions" in r
    assert "errors" in r
    na = len(r["actions"])
    ne = len(r["errors"])
    results.append(("pass", "install_script_dry_run: %d actions, %d errors" % (na, ne)))
except Exception as e:
    results.append(("FAIL", "install_script_dry_run: %s" % e))

# --- allowlist rejections for new tools ---
try:
    asyncio.run(server.apparmor_profile_validate("/home/user/profile"))
    results.append(("FAIL", "apparmor_profile_validate: expected allowlist rejection"))
except ValueError as e:
    results.append(("pass", "apparmor_profile_validate allowlist rejection: %s" % e))

try:
    asyncio.run(server.binary_size_measure("/usr/bin/python3"))
    results.append(("FAIL", "binary_size_measure: expected allowlist rejection"))
except ValueError as e:
    results.append(("pass", "binary_size_measure allowlist rejection: %s" % e))

try:
    asyncio.run(server.incus_smoke_cleanup("production-db"))
    results.append(("FAIL", "incus_smoke_cleanup: expected name guard rejection"))
except ValueError as e:
    results.append(("pass", "incus_smoke_cleanup name guard: %s" % e))

# --- _parse_since ---
now = time.time()
got = server._parse_since("1 hour ago")
if abs(got - (now - 3600)) < 10:
    results.append(("pass", "_parse_since: 1 hour ago"))
else:
    results.append(("FAIL", "_parse_since: unexpected value %s" % got))

# --- new tool allowlist rejections ---
try:
    asyncio.run(server.systemctl_status_fixed("sshd"))
    results.append(("FAIL", "systemctl_status_fixed: expected rejection"))
except ValueError as e:
    results.append(("pass", "systemctl_status_fixed rejection: %s" % e))

try:
    asyncio.run(server.systemctl_restart_scoped("nginx"))
    results.append(("FAIL", "systemctl_restart_scoped: expected rejection"))
except ValueError as e:
    results.append(("pass", "systemctl_restart_scoped rejection: %s" % e))

try:
    asyncio.run(server.tunnel_current_url("badservice"))
    results.append(("FAIL", "tunnel_current_url: expected rejection"))
except ValueError as e:
    results.append(("pass", "tunnel_current_url rejection: %s" % e))

try:
    asyncio.run(server.journal_grep_ventd("1 hour ago", "[invalid"))
    results.append(("FAIL", "journal_grep_ventd: expected invalid pattern error"))
except ValueError as e:
    results.append(("pass", "journal_grep_ventd rejects bad regex: %s" % e))

fail_count = 0
for status, msg in results:
    print("  %s: %s" % (status, msg))
    if status == "FAIL":
        fail_count += 1
sys.exit(fail_count)
PYEOF
'

NEW_TOOL_RC=$?

# ---------------------------------------------------------------------------
_section "A10: Audit log visible on host (via incus exec)"
AUDIT_LINES=$(incus exec "$CONTAINER" -- wc -l /var/log/ops-mcp/audit.jsonl 2>/dev/null | awk '{print $1}')
if [ "${AUDIT_LINES:-0}" -gt 0 ]; then
  _pass "audit log has $AUDIT_LINES entries"
  incus exec "$CONTAINER" -- tail -3 /var/log/ops-mcp/audit.jsonl | while IFS= read -r line; do
    echo "    $line"
  done
else
  _fail "audit log is empty or missing"
fi

# ---------------------------------------------------------------------------
_section "Results"
if [ "${TOOL_RC:-1}" -ne 0 ]; then
  _fail "tool/allowlist tests had failures (rc=$TOOL_RC)"
fi
if [ "${NEW_TOOL_RC:-1}" -ne 0 ]; then
  _fail "new tool smoke tests had failures (rc=$NEW_TOOL_RC)"
fi

echo
echo "PASS: $PASS   FAIL: $FAIL"
if [ "$FAIL" -gt 0 ]; then
  echo "SMOKE: FAIL"
  exit 1
else
  echo "SMOKE: PASS"
  exit 0
fi
