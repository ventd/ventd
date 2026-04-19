#!/usr/bin/env bash
# smoke.sh — Incus smoke test for ops-mcp.
#
# Runs on the host (phoenix-desktop or any Linux box with Incus).
# Creates a disposable Ubuntu 24.04 container, installs ops-mcp inside it,
# exercises all 4 tools + OAuth endpoints, checks the audit log, and verifies
# allowlist rejection. Destroys the container on exit.
#
# Usage:
#   bash .cowork/tools/ops-mcp/smoke.sh
#
# Exit code: 0 = all assertions passed, 1 = at least one failure.
#
# Requirements on the host:
#   - incus (or lxd) CLI and daemon running
#   - Internet access (to pull ubuntu:24.04 image and install packages)
#   - The ops-mcp source tree present at CWD or the path in OPS_MCP_SRC

set -euo pipefail

OPS_MCP_SRC="${OPS_MCP_SRC:-$(cd "$(dirname "$0")" && pwd)}"
CONTAINER="ops-mcp-smoke-$$"
PASS=0
FAIL=0

_pass() { echo "  pass: $*"; ((PASS++)) || true; }
_fail() { echo "  FAIL: $*"; ((FAIL++)) || true; }
_section() { echo; echo "=== $* ==="; }

cleanup() {
  echo
  echo "Destroying container $CONTAINER ..."
  incus delete --force "$CONTAINER" 2>/dev/null || true
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
_section "Launching Incus container: $CONTAINER"
incus launch images:ubuntu/24.04 "$CONTAINER"
# Wait for network
sleep 3
incus exec "$CONTAINER" -- bash -c 'until ping -c1 8.8.8.8 &>/dev/null; do sleep 1; done'
echo "  container up and networked"

# ---------------------------------------------------------------------------
_section "Installing dependencies"
incus exec "$CONTAINER" -- bash -c '
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq python3.11 python3.11-venv python3-pip sudo systemd 2>/dev/null || true
'

# ---------------------------------------------------------------------------
_section "Creating ops-mcp system user"
incus exec "$CONTAINER" -- useradd --system --no-create-home --shell /usr/sbin/nologin ops-mcp

# ---------------------------------------------------------------------------
_section "Copying source into container"
TMPTAR=$(mktemp /tmp/ops-mcp-smoke-XXXXXX.tar.gz)
tar czf "$TMPTAR" -C "$(dirname "$OPS_MCP_SRC")" "$(basename "$OPS_MCP_SRC")"
incus file push "$TMPTAR" "$CONTAINER/tmp/ops-mcp-src.tar.gz"
rm -f "$TMPTAR"
incus exec "$CONTAINER" -- bash -c '
  mkdir -p /opt/ops-mcp
  tar xzf /tmp/ops-mcp-src.tar.gz -C /opt/ops-mcp --strip-components=1
  chown -R ops-mcp:ops-mcp /opt/ops-mcp
'

# ---------------------------------------------------------------------------
_section "Creating directories and env file"
incus exec "$CONTAINER" -- bash -c '
  install -d -o ops-mcp -g ops-mcp -m 755 /var/log/ops-mcp /var/lib/ops-mcp
  cat > /etc/ops-mcp-env <<EOF
OPS_MCP_LOG=DEBUG
OPS_MCP_AUDIT=/var/log/ops-mcp/audit.jsonl
OPS_MCP_HOST=127.0.0.1
OPS_MCP_PORT=8892
EOF
  chmod 640 /etc/ops-mcp-env
  chown root:ops-mcp /etc/ops-mcp-env
'

# ---------------------------------------------------------------------------
_section "Installing Python venv and mcp"
incus exec "$CONTAINER" -- bash -c '
  python3.11 -m venv /opt/ops-mcp/venv
  /opt/ops-mcp/venv/bin/pip install --quiet "mcp>=1.3.0"
'

# ---------------------------------------------------------------------------
_section "Installing sudoers fragment"
incus file push "$OPS_MCP_SRC/ops-mcp.sudoers" "$CONTAINER/etc/sudoers.d/ops-mcp"
incus exec "$CONTAINER" -- chmod 440 /etc/sudoers.d/ops-mcp
incus exec "$CONTAINER" -- visudo -cf /etc/sudoers.d/ops-mcp
if [ $? -eq 0 ]; then
  _pass "sudoers fragment validates"
else
  _fail "sudoers fragment has syntax errors"
fi

# ---------------------------------------------------------------------------
_section "Starting ops-mcp server"
incus exec "$CONTAINER" -- bash -c '
  set -a; source /etc/ops-mcp-env; set +a
  sudo -u ops-mcp /opt/ops-mcp/venv/bin/python /opt/ops-mcp/server.py \
    > /var/log/ops-mcp/server.log 2>&1 &
  echo $! > /tmp/ops-mcp.pid
'
sleep 3

# Verify it is listening
if incus exec "$CONTAINER" -- bash -c 'ss -tlnp | grep -q 8892'; then
  _pass "server listening on 127.0.0.1:8892"
else
  _fail "server not listening on 8892"
  echo "--- server log ---"
  incus exec "$CONTAINER" -- cat /var/log/ops-mcp/server.log || true
  exit 1
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
# We call the Python functions directly (not via MCP HTTP) to test the
# tool logic, allowlist, and audit log without implementing MCP protocol.
incus exec "$CONTAINER" -- bash -c '
set -a; source /etc/ops-mcp-env; set +a
/opt/ops-mcp/venv/bin/python - <<'"'"'PYEOF'"'"'
import sys, os, json
sys.path.insert(0, "/opt/ops-mcp")
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

# A4: allowlist check
for svc in ["spawn-mcp", "ops-mcp", "cloudflared",
            "actions.runner.ventd-ventd.runner1.service"]:
    if _is_allowlisted(svc):
        results.append(("pass", f"allowlist: {svc} accepted"))
    else:
        results.append(("FAIL", f"allowlist: {svc} should be accepted"))

for svc in ["sshd", "nginx", "actions.runner.other.runner.service"]:
    if not _is_allowlisted(svc):
        results.append(("pass", f"allowlist rejection: {svc} correctly blocked"))
    else:
        results.append(("FAIL", f"allowlist: {svc} should be blocked"))

# A5: systemctl_status on ops-mcp (may be inactive in container, but
# the call must not crash and must return the expected keys)
try:
    r = systemctl_status("ops-mcp")
    assert "active" in r, f"missing 'active' key: {r}"
    assert "substate" in r
    assert "main_pid" in r
    assert "started_at" in r
    assert "restart_count" in r
    results.append(("pass", f"systemctl_status returned structured dict: active={r['active']}"))
except Exception as e:
    results.append(("FAIL", f"systemctl_status raised: {e}"))

# A6: systemctl_list_failed must not crash
try:
    r = systemctl_list_failed()
    assert "services" in r, f"missing 'services' key: {r}"
    results.append(("pass", f"systemctl_list_failed returned list (len={len(r['services'])})"))
except Exception as e:
    results.append(("FAIL", f"systemctl_list_failed raised: {e}"))

# A7: journalctl on ops-mcp
try:
    r = journalctl("ops-mcp", lines=10)
    assert "logs" in r
    assert "truncated" in r
    results.append(("pass", "journalctl returned logs dict"))
except Exception as e:
    results.append(("FAIL", f"journalctl raised: {e}"))

# A8: allowlist rejection raises ValueError
try:
    from server import systemctl_restart
    systemctl_restart("sshd")
    results.append(("FAIL", "systemctl_restart(sshd) should have raised ValueError"))
except ValueError as e:
    results.append(("pass", f"systemctl_restart rejection: {e}"))

# A9: audit log grew
try:
    with open(AUDIT_LOG) as f:
        lines = f.readlines()
    assert len(lines) > 0, "audit log is empty"
    last = json.loads(lines[-1])
    assert "ts" in last, "audit entry missing ts"
    results.append(("pass", f"audit log has {len(lines)} entries; last kind={last.get('kind')}"))
except Exception as e:
    results.append(("FAIL", f"audit log check: {e}"))

# Print results
fail_count = 0
for status, msg in results:
    print(f"  {status}: {msg}")
    if status == "FAIL":
        fail_count += 1
sys.exit(fail_count)
PYEOF
'

TOOL_RC=$?

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

echo
echo "PASS: $PASS   FAIL: $FAIL"
if [ "$FAIL" -gt 0 ]; then
  echo "SMOKE: FAIL"
  exit 1
else
  echo "SMOKE: PASS"
  exit 0
fi
