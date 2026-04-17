# spawn-mcp install on phoenix-desktop

One-time setup. Run as a user with sudo on phoenix-desktop
(phoenix@192.168.7.209). Estimated time: 10 minutes including cloudflared
tunnel propagation.

## Why one service user

spawn-mcp and the Claude Code sessions it launches run as the same user
(`cc-runner`). Earlier iterations split them into two system users
(`spawn-mcp` + `cc-runner`) and relied on `sudo -u cc-runner tmux ...`
plus a chown/chmod dance on prompt files. That cross-user boundary was
cosmetic — spawn-mcp had sudo rights to become cc-runner, so any
compromise of spawn-mcp already implied compromise of cc-runner — and
it burned capability grants (CAP_CHOWN, CAP_FOWNER, CAP_SETUID,
CAP_SETGID, CAP_AUDIT_WRITE, CAP_DAC_READ_SEARCH) plus a
`NoNewPrivileges=no` relaxation every time a new failure mode surfaced.
See `.cowork/LESSONS.md` lesson #6 (infra-coherence failures) for the
class. Collapsing the users lets the service run with
`NoNewPrivileges=yes`, an empty ambient/bounding cap set, and zero
sudoers fragments.

## 0. Prerequisites

- `tmux`, `python3.11+`, `git`, `sudo`, `cloudflared`, `systemd` — all present
  on phoenix-desktop per your existing github-mcp-server setup.
- `claude` CLI on PATH for the `cc-runner` user. If not installed:
  ```
  curl -fsSL https://claude.ai/install.sh | sudo -u cc-runner bash
  ```
  (or whatever the current Anthropic-blessed install path is on your distro.)
- Node >= 18 if `claude` CLI is still the Node-based distribution.

## 1. Create the cc-runner service user

```
sudo useradd --system --home /home/cc-runner --shell /bin/bash --create-home cc-runner
```

This is the single identity for both the MCP server and every Claude
Code session it spawns.

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

## 4. Generate a Claude OAuth token for cc-runner

spawn-mcp launches `claude -p` in non-interactive print mode, which
bypasses the theme picker and permission prompts but still needs auth.
The simplest path is a long-lived OAuth token the service forwards via
env. Generate it once, interactively, as cc-runner:

```
sudo -u cc-runner -i
# inside the cc-runner shell:
claude setup-token
# follow the prompts, copy the printed token, then exit
exit
```

The token is printed once and NOT saved to disk by `setup-token`. Put
it in `/etc/spawn-mcp/env` (mode 0640, group cc-runner) under the
`CLAUDE_CODE_OAUTH_TOKEN=` key. See step 5 below.

Alternative: run `sudo -u cc-runner -i claude auth login` interactively
once to seed `~/.claude/` and skip the token approach. Either works;
the OAuth token path is cleaner for automation.

## 5. Install the spawn-mcp service

```
sudo install -d -o cc-runner -g cc-runner -m 755 /opt/spawn-mcp
sudo install -d -o root -g root -m 755 /etc/spawn-mcp
sudo install -d -o cc-runner -g cc-runner -m 755 /var/log/spawn-mcp
sudo install -d -o cc-runner -g cc-runner -m 755 /var/log/spawn-mcp/sessions

sudo cp .cowork/tools/spawn-mcp/server.py /opt/spawn-mcp/server.py
sudo cp .cowork/tools/spawn-mcp/pyproject.toml /opt/spawn-mcp/pyproject.toml
sudo chown cc-runner:cc-runner /opt/spawn-mcp/server.py /opt/spawn-mcp/pyproject.toml

sudo -u cc-runner python3.11 -m venv /opt/spawn-mcp/venv
sudo -u cc-runner /opt/spawn-mcp/venv/bin/pip install 'mcp>=1.3.0'

cat <<EOF | sudo tee /etc/spawn-mcp/env
SPAWN_MCP_OWNER=ventd
SPAWN_MCP_REPO=ventd
SPAWN_MCP_STATE_BRANCH=cowork/state
SPAWN_MCP_WORKTREE=/home/cc-runner/ventd
SPAWN_MCP_CC_BIN=/home/cc-runner/.local/bin/claude
SPAWN_MCP_TMUX_BIN=/usr/bin/tmux
SPAWN_MCP_LOG=INFO
SPAWN_MCP_AUDIT=/var/log/spawn-mcp/audit.jsonl
SPAWN_MCP_SESSION_LOG_DIR=/var/log/spawn-mcp/sessions
CLAUDE_CODE_OAUTH_TOKEN=<paste token from step 4 here>
EOF

sudo chmod 640 /etc/spawn-mcp/env
sudo chown root:cc-runner /etc/spawn-mcp/env

sudo cp .cowork/tools/spawn-mcp/spawn-mcp.service /etc/systemd/system/spawn-mcp.service
sudo cp .cowork/tools/spawn-mcp/spawn-mcp-tunnel.service /etc/systemd/system/spawn-mcp-tunnel.service
```

## 6. Start and verify

```
sudo systemctl daemon-reload
sudo systemctl enable --now spawn-mcp.service
sudo systemctl enable --now spawn-mcp-tunnel.service
sudo journalctl -u spawn-mcp.service -n 20 --no-pager
sudo journalctl -u spawn-mcp-tunnel.service -n 20 --no-pager | grep trycloudflare
```

Confirm the service is running as cc-runner with no elevated capabilities:

```
pid=$(systemctl show -p MainPID --value spawn-mcp.service)
sudo grep -E '^(Uid|Gid|CapEff|NoNewPrivs):' /proc/$pid/status
```

Expect `Uid:` and `Gid:` set to cc-runner's numeric id, `CapEff: 0000000000000000`,
and `NoNewPrivs: 1`.

The tunnel log line will include a hostname like
`https://foo-bar-baz.trycloudflare.com`. Copy it.

## 7. Wire the connector into claude.ai

1. Settings -> Connectors -> Add custom connector.
2. Name: `spawn-mcp` (or `phoenix-spawn`).
3. URL: the trycloudflare hostname from step 6.
4. Auth: OAuth, point at the existing `ventd-cowork` app.
5. Approve + test. Cowork's next session should see `spawn_cc`,
   `list_sessions`, `kill_session`, `tail_session` in its tool list.

## 8. First smoke test

In a Cowork session, run:

    spawn_cc("wd-safety")

This should return `{"status": "spawned", "session_name": "cc-wd-safety-ab12cd", ...}`.

Then verify CC is actually progressing (not stuck on a prompt):

    tail_session("cc-wd-safety-ab12cd", 50)

You should see `claude` output (not a theme-picker welcome screen). If
you see the welcome screen, the OAuth token or `IS_DEMO=1` env is not
reaching the child process; check `sudo journalctl -u spawn-mcp.service`
and `sudo cat /var/log/spawn-mcp/audit.jsonl | tail -5`.

To attach and watch interactively:

    sudo -u cc-runner tmux attach -t cc-wd-safety-ab12cd

(`sudo -u cc-runner` is needed to reach the cc-runner tmux server from
a different shell user — it's not a service-side privilege escalation.)

## 9. Operational checks

- Audit log: `sudo tail -f /var/log/spawn-mcp/audit.jsonl`
- Session logs: `ls /var/log/spawn-mcp/sessions/` — one .log per spawn
- Session list: `sudo -u cc-runner tmux ls`
- Kill a runaway: Cowork can call `kill_session(name)`; manually
  `sudo -u cc-runner tmux kill-session -t cc-<n>`.

## Uninstalling the old two-user model

If upgrading from a host that was set up with a separate `spawn-mcp`
system user and a `/etc/sudoers.d/spawn-mcp` fragment:

```
sudo systemctl revert spawn-mcp.service   # drop any capability drop-ins
sudo rm -f /etc/sudoers.d/spawn-mcp
sudo rm -rf /tmp/cc-runner                # old prompt-handoff dir
# Only after confirming nothing else uses it:
sudo userdel spawn-mcp 2>/dev/null || true
sudo groupdel spawn-mcp 2>/dev/null || true
```

Then follow steps 5-6 to install the unified unit and restart.

## Threat model & limits

- cc-runner has full write access to the ventd repo via its PAT, and
  it is now the MCP server user too. A compromised spawn-mcp process
  already had (via sudo) what a compromised cc-runner has; collapsing
  the users makes that explicit instead of pretending there was a
  boundary. Mitigations are unchanged: OAuth gate on the tunnel,
  systemd hardening directives, audit log. Periodically rotate the PAT.
- The service runs as an unprivileged user with no network
  egress capability beyond the tunnel + GitHub's HTTPS, and with
  `NoNewPrivileges=yes` + empty cap set.
- Prompt-injection from Cowork is not a threat: Cowork is trusted
  (this is the whole point of Path B). If an attacker gains control
  of Cowork's claude.ai session via OAuth token theft, they can
  spawn CC sessions but only against aliases that exist in the repo.
  They cannot inject arbitrary shell, cannot bypass the allowlist,
  and every spawn is logged.
- `CLAUDE_CODE_OAUTH_TOKEN` in /etc/spawn-mcp/env grants CC sessions
  access to your Anthropic subscription. Rotate it if phoenix-desktop
  is ever compromised. `claude setup-token` generates new tokens;
  revoke old ones from claude.ai settings.
