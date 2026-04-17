"""spawn-mcp: MCP server exposing spawn_cc(alias) for Cowork.

Runs on phoenix-desktop. Launches detached tmux sessions that run the
Claude Code CLI with a prompt pulled from the ventd repo's
.cowork/prompts/<alias>.md on the cowork/state branch.

Transport: streamable-http on 127.0.0.1:8891 (override via
SPAWN_MCP_HOST / SPAWN_MCP_PORT). cloudflared tunnel terminates TLS
and exposes a trycloudflare.com hostname that Cowork's claude.ai
connector is pointed at.
"""

from __future__ import annotations

import hashlib
import json
import logging
import os
import re
import secrets
import subprocess
import sys
import time
import urllib.request
from pathlib import Path
from typing import Any

from mcp.server.fastmcp import FastMCP

log = logging.getLogger("spawn-mcp")
logging.basicConfig(
    level=os.environ.get("SPAWN_MCP_LOG", "INFO"),
    format="%(asctime)s %(levelname)s %(name)s %(message)s",
)

# --- Configuration (env, fail loudly if missing) ---------------------------

REPO_OWNER = os.environ.get("SPAWN_MCP_OWNER", "ventd")
REPO_NAME = os.environ.get("SPAWN_MCP_REPO", "ventd")
STATE_BRANCH = os.environ.get("SPAWN_MCP_STATE_BRANCH", "cowork/state")
WORKTREE = Path(os.environ.get("SPAWN_MCP_WORKTREE", "/home/cc-runner/ventd"))
CC_BIN = os.environ.get("SPAWN_MCP_CC_BIN", "claude")
TMUX_BIN = os.environ.get("SPAWN_MCP_TMUX_BIN", "tmux")
AS_USER = os.environ.get("SPAWN_MCP_AS_USER", "cc-runner")
ALIAS_RE = re.compile(r"^[a-zA-Z0-9_-]{1,48}$")
AUDIT_LOG = Path(os.environ.get("SPAWN_MCP_AUDIT", "/var/log/spawn-mcp/audit.jsonl"))
HOST = os.environ.get("SPAWN_MCP_HOST", "127.0.0.1")
PORT = int(os.environ.get("SPAWN_MCP_PORT", "8891"))

AUDIT_LOG.parent.mkdir(parents=True, exist_ok=True)

mcp = FastMCP("spawn-mcp", host=HOST, port=PORT)

# --- Helpers ---------------------------------------------------------------


def _audit(event: dict[str, Any]) -> None:
    event.setdefault("ts", time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
    with AUDIT_LOG.open("a") as f:
        f.write(json.dumps(event) + "\n")


def _github_raw(path: str) -> str | None:
    """Fetch a file from the cowork/state branch via raw.githubusercontent.com.

    Returns the file content, or None if the file does not exist.
    Raises on transport errors so we fail closed.
    """
    url = f"https://raw.githubusercontent.com/{REPO_OWNER}/{REPO_NAME}/{STATE_BRANCH}/{path}"
    req = urllib.request.Request(url, headers={"User-Agent": "spawn-mcp/1"})
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.read().decode("utf-8")
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return None
        raise


def _list_prompt_aliases() -> list[str]:
    """Fetch the directory listing of .cowork/prompts/ via the GitHub tree API.

    Uses an unauthenticated call to the public repo. Rate-limit is 60/h
    unauthenticated per IP; we cache the result for 60 seconds.
    """
    cache = getattr(_list_prompt_aliases, "_cache", None)
    if cache and cache["expires"] > time.time():
        return cache["aliases"]
    url = (
        f"https://api.github.com/repos/{REPO_OWNER}/{REPO_NAME}/contents/"
        f".cowork/prompts?ref={STATE_BRANCH}"
    )
    req = urllib.request.Request(url, headers={"User-Agent": "spawn-mcp/1"})
    with urllib.request.urlopen(req, timeout=10) as resp:
        entries = json.loads(resp.read().decode("utf-8"))
    aliases = [
        Path(e["name"]).stem
        for e in entries
        if e["type"] == "file" and e["name"].endswith(".md") and e["name"] != "INDEX.md"
    ]
    _list_prompt_aliases._cache = {  # type: ignore[attr-defined]
        "expires": time.time() + 60,
        "aliases": aliases,
    }
    return aliases


def _existing_sessions() -> list[str]:
    r = subprocess.run(
        ["sudo", "-u", AS_USER, TMUX_BIN, "ls", "-F", "#S"],
        capture_output=True,
        text=True,
    )
    if r.returncode != 0:
        return []
    return [s.strip() for s in r.stdout.splitlines() if s.strip()]


# --- Tool: spawn_cc --------------------------------------------------------


@mcp.tool()
def spawn_cc(alias: str) -> dict[str, Any]:
    """Spawn a Claude Code session on phoenix-desktop running prompt `<alias>`.

    The alias must correspond to an existing file at
    `.cowork/prompts/<alias>.md` on the `cowork/state` branch of the ventd
    repository. The server fetches the prompt over HTTPS at invocation time
    (no local caching), writes it to a tempfile readable only by cc-runner,
    and launches a detached tmux session that pipes the prompt into
    `claude`.

    Returns a dict with: status, session_name, pid, prompt_sha256.
    """
    if not ALIAS_RE.match(alias):
        _audit({"kind": "reject", "reason": "invalid-alias", "alias": alias})
        return {"status": "rejected", "reason": "alias must match [a-zA-Z0-9_-]{1,48}"}

    aliases = _list_prompt_aliases()
    if alias not in aliases:
        _audit({"kind": "reject", "reason": "unknown-alias", "alias": alias})
        return {
            "status": "rejected",
            "reason": f"alias not in .cowork/prompts/ on {STATE_BRANCH}",
            "known": sorted(aliases),
        }

    prompt = _github_raw(f".cowork/prompts/{alias}.md")
    if prompt is None:
        _audit({"kind": "reject", "reason": "prompt-404", "alias": alias})
        return {"status": "rejected", "reason": "prompt file 404 at raw.githubusercontent"}

    prompt_sha = hashlib.sha256(prompt.encode("utf-8")).hexdigest()
    shortid = secrets.token_hex(3)
    session_name = f"cc-{alias}-{shortid}"

    # Write prompt to a file cc-runner can read. /tmp/cc-runner/ is chmod 700,
    # owned by cc-runner, created by the systemd unit's StateDirectory setting.
    prompt_path = Path("/tmp/cc-runner") / f"{session_name}.md"
    prompt_path.parent.mkdir(parents=True, exist_ok=True)
    prompt_path.write_text(prompt)
    subprocess.run(["chown", f"{AS_USER}:{AS_USER}", str(prompt_path)], check=True)
    subprocess.run(["chmod", "600", str(prompt_path)], check=True)

    # Refresh worktree to latest main before dispatch.
    fetch = subprocess.run(
        ["sudo", "-u", AS_USER, "git", "-C", str(WORKTREE), "fetch", "origin", "main", "--depth=50"],
        capture_output=True,
        text=True,
    )
    if fetch.returncode != 0:
        log.warning("git fetch failed: %s", fetch.stderr)

    # Spawn. `claude --print` or `claude -p` reads the prompt from a file and
    # starts the session. If the CC CLI lacks a file flag in the installed
    # version, fall back to `cat prompt | claude`.
    cmd = [
        "sudo",
        "-u",
        AS_USER,
        TMUX_BIN,
        "new-session",
        "-d",
        "-s",
        session_name,
        "-c",
        str(WORKTREE),
        f"cat {prompt_path} | {CC_BIN}; sleep 5",
    ]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        _audit(
            {
                "kind": "error",
                "alias": alias,
                "session": session_name,
                "stderr": r.stderr,
            }
        )
        return {"status": "error", "stderr": r.stderr, "stdout": r.stdout}

    _audit(
        {
            "kind": "spawn",
            "alias": alias,
            "session": session_name,
            "prompt_sha256": prompt_sha,
        }
    )
    return {
        "status": "spawned",
        "session_name": session_name,
        "prompt_sha256": prompt_sha,
        "worktree": str(WORKTREE),
        "hint": f"attach on phoenix-desktop: sudo -u {AS_USER} tmux attach -t {session_name}",
    }


# --- Tool: list_sessions ---------------------------------------------------


@mcp.tool()
def list_sessions() -> dict[str, Any]:
    """List currently running CC tmux sessions on phoenix-desktop."""
    sessions = _existing_sessions()
    return {"sessions": sessions, "count": len(sessions)}


# --- Tool: kill_session ----------------------------------------------------


@mcp.tool()
def kill_session(session_name: str) -> dict[str, Any]:
    """Kill a named CC tmux session. Only sessions prefixed `cc-` are allowed."""
    if not session_name.startswith("cc-") or not re.match(r"^cc-[a-zA-Z0-9_-]+$", session_name):
        return {"status": "rejected", "reason": "only cc-<alias>-<shortid> sessions may be killed"}
    r = subprocess.run(
        ["sudo", "-u", AS_USER, TMUX_BIN, "kill-session", "-t", session_name],
        capture_output=True,
        text=True,
    )
    _audit({"kind": "kill", "session": session_name, "rc": r.returncode})
    return {"status": "killed" if r.returncode == 0 else "error", "stderr": r.stderr}


# --- Tool: tail_session ----------------------------------------------------


@mcp.tool()
def tail_session(session_name: str, lines: int = 200) -> dict[str, Any]:
    """Dump the last N lines of a CC session's scrollback so Cowork can read CC's output."""
    if not re.match(r"^cc-[a-zA-Z0-9_-]+$", session_name):
        return {"status": "rejected", "reason": "invalid session name"}
    lines = max(1, min(lines, 2000))
    r = subprocess.run(
        [
            "sudo",
            "-u",
            AS_USER,
            TMUX_BIN,
            "capture-pane",
            "-p",
            "-S",
            f"-{lines}",
            "-t",
            session_name,
        ],
        capture_output=True,
        text=True,
    )
    return {
        "status": "ok" if r.returncode == 0 else "error",
        "scrollback": r.stdout,
        "stderr": r.stderr,
    }


if __name__ == "__main__":
    log.info("spawn-mcp starting on %s:%d; worktree=%s user=%s", HOST, PORT, WORKTREE, AS_USER)
    mcp.run(transport="streamable-http")
