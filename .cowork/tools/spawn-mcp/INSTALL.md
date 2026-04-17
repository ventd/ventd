# spawn-mcp install on phoenix-desktop

One-time setup. Run as a user with sudo on phoenix-desktop
(phoenix@192.168.7.209). Estimated time: 10 minutes including cloudflared
tunnel propagation.

## 0. Prerequisites

- `tmux`, `python3.11+`, `git`, `sudo`, `cloudflared`, `systemd` — all present
  on phoenix-desktop per your existing github-mcp-server setup.
- `claude` CLI on PATH for the `cc-runner` user. If not installed:
  ```
  curl -fsSL https://claude.ai/install.sh | sudo -u cc-runner bash
  ```
  (or whatever the current Anthropic-blessed install path is on your distro.)
- Node >= 18 if `claude` CLI is still the Node-based distribution.

## 1. Create the two service users

```
sudo useradd --system --home /opt/spawn-mcp --shell /usr/sbin/nologin spawn-mcp
sudo useradd --system --home /home/cc-runner --shell /bin/bash --create-home cc-runner
```

## 2. Clone the ventd worktree into cc-runner's home

```
sudo -u cc-runner git clone https://github.com/ventd/ventd /home/cc-runner/ventd
```

This is cc-runner's read-only(-ish) worktree where Claude Code sessions
operate. CC sessions will branch + push from here under cc-runner's
credentials (configure below).

## 3. Configure cc-runner's git + gh credentials

cc-runner needs to push branches and create PRs. Reuse the fine-grained
PAT you already use for github-mcp-server, or mint a dedicated one with:
contents:write, pull-requests:write, workflows (if you want CC to touch
.github/workflows), actions:read.

```
sudo -u cc-runner bash -c 'echo <PAT> | gh auth login --with-token'
sudo -u cc-runner git config --global user.name "cc-runner"
sudo -u cc-runner git config --global user.email "cc-runner@localhost"
```

## 4. Install the spawn-mcp service

```
sudo install -d -o spawn-mcp -g spawn-mcp -m 755 /opt/spawn-mcp
sudo install -d -o root -g root -m 755 /etc/spawn-mcp
sudo install -d -o spawn-mcp -g spawn-mcp -m 755 /var/log/spawn-mcp
sudo install -d -o cc-runner -g cc-runner -m 700 /tmp/cc-runner

sudo cp .cowork/tools/spawn-mcp/server.py /opt/spawn-mcp/server.py
sudo cp .cowork/tools/spawn-mcp/pyproject.toml /opt/spawn-mcp/pyproject.toml

sudo -u spawn-mcp python3.11 -m venv /opt/spawn-mcp/venv
sudo -u spawn-mcp /opt/spawn-mcp/venv/bin/pip install 'mcp>=1.3.0'

cat <<EOF | sudo tee /etc/spawn-mcp/env
SPAWN_MCP_OWNER=ventd
SPAWN_MCP_REPO=ventd
SPAWN_MCP_STATE_BRANCH=cowork/state
SPAWN_MCP_WORKTREE=/home/cc-runner/ventd
SPAWN_MCP_CC_BIN=/home/cc-runner/.local/bin/claude
SPAWN_MCP_TMUX_BIN=/usr/bin/tmux
SPAWN_MCP_AS_USER=cc-runner
SPAWN_MCP_LOG=INFO
SPAWN_MCP_AUDIT=/var/log/spawn-mcp/audit.jsonl
EOF

sudo chmod 640 /etc/spawn-mcp/env
sudo chown root:spawn-mcp /etc/spawn-mcp/env

sudo cp .cowork/tools/spawn-mcp/spawn-mcp.service /etc/systemd/system/spawn-mcp.service
sudo cp .cowork/tools/spawn-mcp/spawn-mcp-tunnel.service /etc/systemd/system/spawn-mcp-tunnel.service
sudo cp .cowork/tools/spawn-mcp/sudoers.d-spawn-mcp /etc/sudoers.d/spawn-mcp
sudo chmod 440 /etc/sudoers.d/spawn-mcp
sudo visudo -c
```

## 5. Start and verify

```
sudo systemctl daemon-reload
sudo systemctl enable --now spawn-mcp.service
sudo systemctl enable --now spawn-mcp-tunnel.service
sudo journalctl -u spawn-mcp.service -n 20 --no-pager
sudo journalctl -u spawn-mcp-tunnel.service -n 20 --no-pager | grep trycloudflare
```

The tunnel log line will include a hostname like
`https://foo-bar-baz.trycloudflare.com`. Copy it.

## 6. Wire the connector into claude.ai

1. Settings -> Connectors -> Add custom connector.
2. Name: `spawn-mcp` (or `phoenix-spawn`).
3. URL: the trycloudflare hostname from step 5.
4. Auth: OAuth, point at the existing `ventd-cowork` app.
5. Approve + test. Cowork's next session should see `spawn_cc`,
   `list_sessions`, `kill_session`, `tail_session` in its tool list.

## 7. First smoke test

In a Cowork session, run:

    spawn_cc("wd-safety")

This should return `{"status": "spawned", "session_name": "cc-wd-safety-ab12cd", ...}`.
Then on phoenix-desktop:

    sudo -u cc-runner tmux attach -t cc-wd-safety-ab12cd

to watch the CC agent work.

## 8. Operational checks

- Audit log: `sudo tail -f /var/log/spawn-mcp/audit.jsonl`
- Session list: `sudo -u cc-runner tmux ls`
- Kill a runaway: Cowork can call `kill_session(name)`; manually
  `sudo -u cc-runner tmux kill-session -t cc-<name>`.

## Threat model & limits

- cc-runner has full write access to the ventd repo via its PAT.
  A compromised spawn-mcp = a compromised PAT. Mitigations: OAuth gate
  on the tunnel, narrow sudoers, systemd hardening directives, audit
  log. Periodically rotate the PAT.
- spawn-mcp itself runs as an unprivileged user with no network
  egress capability beyond the tunnel + GitHub's HTTPS.
- Prompt-injection from Cowork is not a threat: Cowork is trusted
  (this is the whole point of Path B). If an attacker gains control
  of Cowork's claude.ai session via OAuth token theft, they can
  spawn CC sessions but only against aliases that exist in the repo.
  They cannot inject arbitrary shell, cannot bypass the allowlist,
  and every spawn is logged.
