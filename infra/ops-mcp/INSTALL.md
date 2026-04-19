# ops-mcp install on phoenix-desktop

One-time setup. Run as a user with sudo on phoenix-desktop
(phoenix@192.168.7.209). Estimated time: 5 minutes.

ops-mcp runs as a dedicated `ops-mcp` system user with narrow sudoers rules
that cover only the allowlisted systemctl/journalctl operations. It uses the
same cloudflared quick-tunnel + OAuth stub pattern as spawn-mcp.

## 0. Prerequisites

- `python3.11+`, `cloudflared`, `systemd` — present on phoenix-desktop.
- Port 8892 free on 127.0.0.1 (spawn-mcp uses 8891, github-mcp uses 8893).

## 1. Create the ops-mcp system user

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin ops-mcp
```

ops-mcp has no home directory and cannot log in interactively. It runs only
via the systemd unit.

## 2. Install the service

```bash
sudo install -d -o ops-mcp -g ops-mcp -m 755 /opt/ops-mcp
sudo install -d -o root    -g root    -m 755 /etc/ops-mcp
sudo install -d -o ops-mcp -g ops-mcp -m 755 /var/log/ops-mcp
sudo install -d -o ops-mcp -g ops-mcp -m 755 /var/lib/ops-mcp

sudo install -o ops-mcp -g ops-mcp -m 644 \
    infra/ops-mcp/server.py \
    infra/ops-mcp/pyproject.toml \
    /opt/ops-mcp/

sudo -u ops-mcp python3.11 -m venv /opt/ops-mcp/venv
sudo -u ops-mcp /opt/ops-mcp/venv/bin/pip install 'mcp>=1.3.0'
```

## 3. Write the env file

```bash
cat <<EOF | sudo tee /etc/ops-mcp/env
OPS_MCP_LOG=INFO
OPS_MCP_AUDIT=/var/log/ops-mcp/audit.jsonl
OPS_MCP_HOST=127.0.0.1
OPS_MCP_PORT=8892
EOF

sudo chmod 640 /etc/ops-mcp/env
sudo chown root:ops-mcp /etc/ops-mcp/env
```

## 4. Install sudoers fragment

```bash
sudo install -m 440 infra/ops-mcp/ops-mcp.sudoers /etc/sudoers.d/ops-mcp
# Validate before the next step — visudo will catch syntax errors.
sudo visudo -cf /etc/sudoers.d/ops-mcp
```

Confirm the grants look right:

```bash
sudo -l -U ops-mcp
```

Expected output includes NOPASSWD entries for `/bin/systemctl restart <each
allowlisted service>`, `/bin/systemctl show *`, `/bin/systemctl --failed *`,
and `/bin/journalctl`.

## 5. Install logrotate

```bash
sudo install -m 644 infra/ops-mcp/logrotate.d/ops-mcp /etc/logrotate.d/ops-mcp
```

The audit log rotates weekly, keeps 12 weeks, and compresses old files.

## 6. Install systemd units

```bash
sudo cp infra/ops-mcp/ops-mcp.service /etc/systemd/system/
sudo cp infra/ops-mcp/ops-mcp-tunnel.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ops-mcp.service
sudo systemctl enable --now ops-mcp-tunnel.service
```

## 7. Verify

```bash
sudo systemctl status ops-mcp.service
sudo journalctl -u ops-mcp.service -n 20 --no-pager
# Confirm tunnel hostname:
sudo journalctl -u ops-mcp-tunnel.service -n 20 --no-pager | grep trycloudflare
```

Confirm no elevated capabilities:

```bash
pid=$(systemctl show -p MainPID --value ops-mcp.service)
sudo grep -E '^(Uid|Gid|CapEff|NoNewPrivs):' /proc/$pid/status
```

Expected: `Uid`/`Gid` = ops-mcp's numeric id, `CapEff: 0000000000000000`.
`NoNewPrivs` will be `0` (we need NoNewPrivileges=no for sudo SUID).

## 8. Wire into Atlas

1. Settings → Connectors → Add custom connector.
2. Name: `ops-mcp`.
3. URL: the trycloudflare hostname from step 7.
4. Auth: OAuth.
5. Test: `systemctl_status("spawn-mcp")` should return `{"active": true, ...}`.

## 9. Test allowlist rejection

```bash
# Should return a ValueError (rejected, not a crash):
sudo -u ops-mcp /opt/ops-mcp/venv/bin/python - <<'EOF'
import sys
sys.path.insert(0, "/opt/ops-mcp")
from server import systemctl_restart
try:
    systemctl_restart("sshd")
    print("FAIL: expected ValueError")
except ValueError as e:
    print(f"pass: rejection works: {e}")
EOF
```

## 10. Operational checks

- Audit log: `sudo tail -f /var/log/ops-mcp/audit.jsonl`
- Service restart: `sudo systemctl restart ops-mcp.service`
- Tunnel hostname: `sudo journalctl -u ops-mcp-tunnel.service -n 5 --no-pager`

## Threat model

- ops-mcp can restart only the listed services. A compromised ops-mcp cannot
  restart sshd, nginx, or any service not in the allowlist.
- Read operations (journalctl, systemctl show, systemctl --failed) are also
  allowlisted in Python before the sudo call, but the sudoers rules for those
  are broader (any arguments) because argument-level restriction in sudoers is
  less reliable than application-layer enforcement.
- The audit log records every tool call and every rejection. Monitor with:
  `sudo tail -f /var/log/ops-mcp/audit.jsonl | jq .`
- The service has `CapabilityBoundingSet=` empty and `ProtectSystem=strict`.
  The only relaxation vs. spawn-mcp is `NoNewPrivileges=no` (needed for sudo).
