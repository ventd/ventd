"""spawn-mcp: MCP server exposing spawn_cc(alias) for Cowork.

Runs on phoenix-desktop. Launches detached tmux sessions that run the
Claude Code CLI in non-interactive print mode with a prompt pulled
from the ventd repo's .cowork/prompts/<alias>.md on cowork/state.

Transport: streamable-http on 127.0.0.1:8891 (override via
SPAWN_MCP_HOST / SPAWN_MCP_PORT). cloudflared tunnel terminates TLS
and exposes a trycloudflare.com hostname that Cowork's claude.ai
connector is pointed at.

Security:

- DNS-rebinding protection is deliberately disabled. The SDK's
  allowed_hosts check is a literal string-equals match (no wildcard
  matching except ports like base:*), and trycloudflare quick-tunnel
  hostnames rotate on every cloudflared restart, so there is no
  reasonable value to put in allowed_hosts that would admit legitimate
  traffic. The real access controls are:
    1. Server binds to 127.0.0.1 only — no direct external reach.
    2. cloudflared tunnel terminates the only external path.
    3. OAuth on claude.ai's custom-connector layer authenticates Cowork.
  If you later move to a named tunnel with a stable hostname, flip
  SPAWN_MCP_ENABLE_DNS_REBINDING_PROTECTION=1 and set
  SPAWN_MCP_ALLOWED_HOSTS="your-host:*" to tighten.

Claude CLI invocation:

- Uses `claude --dangerously-skip-permissions -p < prompt.md` so the
  CLI runs in non-interactive print mode. This is the documented
  pattern for scripted / autonomous runs and bypasses the interactive
  onboarding flow (theme picker, permission prompts, trust dialog).
- IS_DEMO=1 is injected into the child environment as belt-and-braces
  to skip onboarding on fresh installs where ~/.claude.json has not
  been seeded.
- CLAUDE_CODE_OAUTH_TOKEN, if set in /etc/spawn-mcp/env, is forwarded
  to the CLI so a fresh service user without an interactive `claude
  auth login` session can still authenticate.
"""

from __future__ import annotations

import hashlib
import json
import logging
import os
import re
import secrets
import shlex
import subprocess
import sys
import time
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

from mcp.server.fastmcp import FastMCP
from mcp.server.transport_security import TransportSecuritySettings
from starlette.requests import Request
from starlette.responses import JSONResponse, RedirectResponse

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
CC_WORKTREE_BASE = os.environ.get("SPAWN_MCP_CC_WORKTREE_BASE", "/tmp")
CC_BIN = os.environ.get("SPAWN_MCP_CC_BIN", "claude")
TMUX_BIN = os.environ.get("SPAWN_MCP_TMUX_BIN", "tmux")
ALIAS_RE = re.compile(r"^[a-zA-Z0-9_-]{1,48}$")
AUDIT_LOG = Path(os.environ.get("SPAWN_MCP_AUDIT", "/var/log/spawn-mcp/audit.jsonl"))
PROMPT_DIR = Path(os.environ.get("SPAWN_MCP_PROMPT_DIR", "/tmp/spawn-mcp"))
INLINE_PROMPT_DIR = Path(os.environ.get("SPAWN_MCP_INLINE_PROMPT_DIR", "/tmp/cc-prompts"))
SESSION_LOG_DIR = Path(os.environ.get("SPAWN_MCP_SESSION_LOG_DIR", "/var/log/spawn-mcp/sessions"))
HOST = os.environ.get("SPAWN_MCP_HOST", "127.0.0.1")
PORT = int(os.environ.get("SPAWN_MCP_PORT", "8891"))
BATCH_ENABLED = os.environ.get("SPAWN_MCP_BATCH_ENABLED", "0") in ("1", "true", "yes")

# Optional: forward a long-lived OAuth token to the claude CLI so a fresh
# service user without interactive `claude auth login` can authenticate.
# Generate with `claude setup-token` as cc-runner on phoenix-desktop once,
# then set in /etc/spawn-mcp/env. Absent => CLI uses keychain / ~/.claude/.
CLAUDE_CODE_OAUTH_TOKEN = os.environ.get("CLAUDE_CODE_OAUTH_TOKEN", "")

# DNS-rebinding protection: off by default because trycloudflare hostnames
# rotate and the SDK does literal string matching on Host. See module docstring.
ENABLE_DNS_REBINDING_PROTECTION = os.environ.get(
    "SPAWN_MCP_ENABLE_DNS_REBINDING_PROTECTION", "0"
) in ("1", "true", "yes")
ALLOWED_HOSTS = [h.strip() for h in os.environ.get("SPAWN_MCP_ALLOWED_HOSTS", "").split(",") if h.strip()]
ALLOWED_ORIGINS = [o.strip() for o in os.environ.get("SPAWN_MCP_ALLOWED_ORIGINS", "").split(",") if o.strip()]

AUDIT_LOG.parent.mkdir(parents=True, exist_ok=True)
SESSION_LOG_DIR.mkdir(parents=True, exist_ok=True)
INLINE_PROMPT_DIR.mkdir(parents=True, exist_ok=True)

mcp = FastMCP(
    "spawn-mcp",
    host=HOST,
    port=PORT,
    streamable_http_path="/",
    transport_security=TransportSecuritySettings(
        enable_dns_rebinding_protection=ENABLE_DNS_REBINDING_PROTECTION,
        allowed_hosts=ALLOWED_HOSTS,
        allowed_origins=ALLOWED_ORIGINS,
    ),
)

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
        [TMUX_BIN, "ls", "-F", "#S"],
        capture_output=True,
        text=True,
    )
    if r.returncode != 0:
        return []
    return [s.strip() for s in r.stdout.splitlines() if s.strip()]


def _session_exists(session_name: str) -> bool:
    return session_name in _existing_sessions()


def _has_per_session_worktrees() -> bool:
    return os.access(CC_WORKTREE_BASE, os.W_OK)


def _parse_exit_code(content: str) -> int | None:
    m = re.search(r"claude exited rc=(\d+)", content)
    return int(m.group(1)) if m else None


def _extract_pr_url(content: str) -> str | None:
    m = re.search(r"https://github\.com/\S+/pull/\d+", content)
    return m.group(0) if m else None


def _build_worktree_cmd(session_name: str, prompt_path: Path, session_log: Path) -> str:
    """Return the shell command tmux should run in the new session.

    Creates a per-session git worktree at CC_WORKTREE_BASE/cc-wt-<session_name>,
    runs claude from that isolated tree, then removes the worktree on exit.
    If worktree creation fails (disk full, inode exhaustion, git error), falls
    back to running claude in the main WORKTREE with a structured-log warning.
    """
    wt = shlex.quote(f"{CC_WORKTREE_BASE}/cc-wt-{session_name}")
    main_wt = shlex.quote(str(WORKTREE))
    log_q = shlex.quote(str(session_log))
    prompt_q = shlex.quote(str(prompt_path))
    cc_q = shlex.quote(CC_BIN)

    env_parts = ["IS_DEMO=1"]
    if CLAUDE_CODE_OAUTH_TOKEN:
        env_parts.append(f"CLAUDE_CODE_OAUTH_TOKEN={shlex.quote(CLAUDE_CODE_OAUTH_TOKEN)}")
    env_prefix = " ".join(env_parts)

    claude_run = (
        f"{env_prefix} {cc_q} --dangerously-skip-permissions -p "
        f"< {prompt_q} 2>&1 | tee -a {log_q}"
    )
    marker = (
        f"echo '=== spawn-mcp: claude exited rc='$?' at '$(date -Iseconds)"
        f" >> {log_q}"
    )
    cleanup = f"cd {main_wt} && git worktree remove --force {wt} 2>/dev/null || true"

    # Try to create and use a per-session worktree; fall back to main on failure.
    return (
        f"if cd {main_wt} && git fetch --quiet origin"
        f" && git worktree add --detach {wt} origin/main; then"
        f" cd {wt} && {claude_run}; {marker}; {cleanup}; sleep 30;"
        f" else"
        f" echo 'spawn-mcp: worktree create failed, falling back to main worktree'"
        f" >> {log_q};"
        f" cd {main_wt} && {claude_run}; {marker}; sleep 30;"
        f" fi"
    )


# --- Tool: spawn_cc --------------------------------------------------------


@mcp.tool()
def spawn_cc(alias: str) -> dict[str, Any]:
    """Spawn a Claude Code session on phoenix-desktop running prompt `<alias>`.

    The alias must correspond to an existing file at
    `.cowork/prompts/<alias>.md` on the `cowork/state` branch of the ventd
    repository. The server fetches the prompt over HTTPS at invocation time
    (no local caching), writes it to a 0600 tempfile owned by the service
    user, and launches a detached tmux session that runs `claude -p` in
    non-interactive print mode.

    Returns a dict with: status, session_name, prompt_sha256, session_log.
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

    # Write prompt to a 0600 file owned by the service user.
    PROMPT_DIR.mkdir(parents=True, exist_ok=True)
    prompt_path = PROMPT_DIR / f"{session_name}.md"
    fd = os.open(str(prompt_path), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        os.write(fd, prompt.encode("utf-8"))
    finally:
        os.close(fd)

    # Per-session log captures stdout+stderr from claude for post-hoc
    # inspection. Distinct from audit.jsonl which logs spawn-mcp's own
    # actions.
    session_log = SESSION_LOG_DIR / f"{session_name}.log"

    # git fetch is handled inside the per-session shell wrapper so each
    # worktree starts from a fresh origin/main. The Python-level fetch is
    # removed to avoid the double-fetch latency.
    wt_path = f"{CC_WORKTREE_BASE}/cc-wt-{session_name}"

    cmd = [
        TMUX_BIN,
        "new-session",
        "-d",
        "-s",
        session_name,
        "-c",
        str(WORKTREE),
        _build_worktree_cmd(session_name, prompt_path, session_log),
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
            "session_log": str(session_log),
            "worktree_path": wt_path,
            "oauth_token_set": bool(CLAUDE_CODE_OAUTH_TOKEN),
        }
    )
    return {
        "status": "spawned",
        "session_name": session_name,
        "prompt_sha256": prompt_sha,
        "worktree_path": wt_path,
        "session_log": str(session_log),
        "hint": f"attach on phoenix-desktop: sudo -u cc-runner tmux attach -t {session_name}",
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
        [TMUX_BIN, "kill-session", "-t", session_name],
        capture_output=True,
        text=True,
    )
    _audit({"kind": "kill", "session": session_name, "rc": r.returncode})
    return {"status": "killed" if r.returncode == 0 else "error", "stderr": r.stderr}


# --- Tool: tail_session ----------------------------------------------------


@mcp.tool()
def tail_session(session_name: str, lines: int = 200) -> dict[str, Any]:
    """Dump the last N lines of a CC session's scrollback or persistent log.

    Prefers the session log file if present (survives tmux pane scroll-back
    limits and tmux session exit). Falls back to tmux capture-pane if the
    log is missing.
    """
    if not re.match(r"^cc-[a-zA-Z0-9_-]+$", session_name):
        return {"status": "rejected", "reason": "invalid session name"}
    lines = max(1, min(lines, 2000))

    # Prefer the persistent session log.
    session_log = SESSION_LOG_DIR / f"{session_name}.log"
    if session_log.exists():
        try:
            with session_log.open("r") as f:
                content = f.readlines()
            return {
                "status": "ok",
                "source": "session_log",
                "scrollback": "".join(content[-lines:]),
                "stderr": "",
            }
        except OSError as e:
            log.warning("failed to read session log %s: %s", session_log, e)

    # Fallback: tmux capture-pane.
    r = subprocess.run(
        [
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
        "source": "tmux",
        "scrollback": r.stdout,
        "stderr": r.stderr,
    }


# --- Tool: spawn_cc_inline -------------------------------------------------


@mcp.tool()
def spawn_cc_inline(prompt_text: str, alias_hint: str | None = None) -> dict[str, Any]:
    """Spawn CC with an inline prompt, bypassing the cowork/state alias fetch.

    Eliminates the ~5 min CDN cache delay on fresh or just-edited prompts.
    prompt_text is the full prompt body in the same format as
    .cowork/prompts/*.md. alias_hint is an optional short name used in the
    session name; defaults to "inline".

    Returns: same shape as spawn_cc.
    """
    shortid = secrets.token_hex(3)
    hint = re.sub(r"[^a-zA-Z0-9_-]", "-", alias_hint or "inline")[:24].strip("-") or "inline"
    session_name = f"cc-{hint}-{shortid}"
    prompt_sha = hashlib.sha256(prompt_text.encode("utf-8")).hexdigest()

    INLINE_PROMPT_DIR.mkdir(parents=True, exist_ok=True)
    prompt_path = INLINE_PROMPT_DIR / f"{session_name}.md"
    fd = os.open(str(prompt_path), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        os.write(fd, prompt_text.encode("utf-8"))
    finally:
        os.close(fd)

    session_log = SESSION_LOG_DIR / f"{session_name}.log"
    wt_path = f"{CC_WORKTREE_BASE}/cc-wt-{session_name}"

    cmd = [
        TMUX_BIN,
        "new-session",
        "-d",
        "-s",
        session_name,
        "-c",
        str(WORKTREE),
        _build_worktree_cmd(session_name, prompt_path, session_log),
    ]
    r = subprocess.run(cmd, capture_output=True, text=True)
    if r.returncode != 0:
        _audit({"kind": "error", "alias_hint": hint, "session": session_name, "stderr": r.stderr})
        return {"status": "error", "stderr": r.stderr, "stdout": r.stdout}

    _audit({
        "kind": "spawn_inline",
        "alias_hint": hint,
        "session": session_name,
        "prompt_sha256": prompt_sha,
        "session_log": str(session_log),
        "worktree_path": wt_path,
    })
    return {
        "status": "spawned",
        "session_name": session_name,
        "prompt_sha256": prompt_sha,
        "worktree_path": wt_path,
        "session_log": str(session_log),
        "hint": f"attach on phoenix-desktop: sudo -u cc-runner tmux attach -t {session_name}",
    }


# --- Tool: wait_for_session ------------------------------------------------


@mcp.tool()
def wait_for_session(session_name: str, timeout_s: int = 1800, poll_interval_s: int = 15) -> dict[str, Any]:
    """Block until the named CC session exits or the timeout expires.

    Polls tmux every poll_interval_s seconds. When the session disappears,
    reads the session log for exit code and any PR URL opened by the session.

    Returns: exit_code (int or null), duration_s, last_output (up to 2000 chars),
             pr_opened (URL string or null), timed_out (bool).
    """
    if not re.match(r"^cc-[a-zA-Z0-9_-]+$", session_name):
        return {"status": "rejected", "reason": "invalid session name"}
    timeout_s = max(1, min(timeout_s, 7200))
    poll_interval_s = max(5, min(poll_interval_s, 300))

    start = time.time()
    while (time.time() - start) < timeout_s:
        if not _session_exists(session_name):
            log_path = SESSION_LOG_DIR / f"{session_name}.log"
            try:
                content = log_path.read_text()
            except FileNotFoundError:
                content = ""
            return {
                "exit_code": _parse_exit_code(content),
                "duration_s": int(time.time() - start),
                "last_output": content[-2000:] if content else "",
                "pr_opened": _extract_pr_url(content),
                "timed_out": False,
            }
        time.sleep(poll_interval_s)

    return {
        "exit_code": None,
        "duration_s": timeout_s,
        "last_output": "",
        "pr_opened": None,
        "timed_out": True,
    }


# --- Tool: spawn_cc_batch --------------------------------------------------


@mcp.tool()
def spawn_cc_batch(aliases: list[str], max_parallel: int = 3) -> list[dict[str, Any]]:
    """Fire up to max_parallel CC sessions in parallel, one per alias.

    Requires SPAWN_MCP_BATCH_ENABLED=true (default off) and IMPROV-A
    per-session worktree support (CC_WORKTREE_BASE must be writable).

    Returns: list of spawn results in the same shape as spawn_cc.
    """
    if not BATCH_ENABLED:
        return [{"status": "rejected", "reason": "spawn_cc_batch disabled; set SPAWN_MCP_BATCH_ENABLED=true to enable"}]
    if not _has_per_session_worktrees():
        return [{"status": "rejected", "reason": "spawn_cc_batch requires IMPROV-A worktree isolation; CC_WORKTREE_BASE not writable"}]

    results = []
    for alias in aliases[:max_parallel]:
        results.append(spawn_cc(alias))
        time.sleep(0.5)
    return results


# --- OAuth shim (to satisfy claude.ai's custom-connector OAuth flow) -------
# The trycloudflare tunnel + claude.ai connector registration is the
# security boundary. These endpoints auto-approve; no PKCE verification,
# no client_id check, no token validation on /mcp. If this server ever
# moves behind a stable hostname we should replace this with a real AS.


def _public_origin(request: Request) -> str:
    proto = request.headers.get("x-forwarded-proto", "https")
    host = request.headers.get("x-forwarded-host") or request.headers.get("host", "")
    return f"{proto}://{host}"


@mcp.custom_route("/.well-known/oauth-authorization-server", methods=["GET"])
async def _oauth_as_metadata(request: Request) -> JSONResponse:
    origin = _public_origin(request)
    return JSONResponse(
        {
            "issuer": origin,
            "authorization_endpoint": f"{origin}/authorize",
            "token_endpoint": f"{origin}/token",
            "response_types_supported": ["code"],
            "grant_types_supported": ["authorization_code"],
            "code_challenge_methods_supported": ["S256", "plain"],
            "token_endpoint_auth_methods_supported": ["none"],
            "scopes_supported": ["mcp"],
        }
    )


@mcp.custom_route("/.well-known/oauth-protected-resource", methods=["GET"])
async def _oauth_pr_metadata(request: Request) -> JSONResponse:
    origin = _public_origin(request)
    return JSONResponse(
        {
            "resource": origin,
            "authorization_servers": [origin],
        }
    )


@mcp.custom_route("/authorize", methods=["GET"])
async def _authorize(request: Request) -> RedirectResponse:
    redirect_uri = request.query_params.get("redirect_uri", "")
    state = request.query_params.get("state", "")
    code = secrets.token_urlsafe(24)
    params = urllib.parse.urlencode({"code": code, "state": state})
    sep = "&" if "?" in redirect_uri else "?"
    target = f"{redirect_uri}{sep}{params}"
    _audit({"kind": "oauth-authorize", "redirect_uri": redirect_uri, "state_len": len(state)})
    return RedirectResponse(url=target, status_code=302)


@mcp.custom_route("/token", methods=["POST"])
async def _token(request: Request) -> JSONResponse:
    _audit({"kind": "oauth-token"})
    return JSONResponse(
        {
            "access_token": secrets.token_urlsafe(32),
            "token_type": "Bearer",
            "expires_in": 3600,
            "scope": "mcp",
        }
    )


@mcp.custom_route("/register", methods=["POST"])
async def _register(request: Request) -> JSONResponse:
    # RFC 7591 dynamic client registration stub. Claude.ai may probe this
    # if it decides to register a client dynamically. Always accept.
    try:
        body = await request.json()
    except Exception:
        body = {}
    _audit({"kind": "oauth-register", "body_keys": sorted(body.keys()) if isinstance(body, dict) else []})
    client_id = body.get("client_id") if isinstance(body, dict) else None
    return JSONResponse(
        {
            "client_id": client_id or secrets.token_urlsafe(16),
            "client_id_issued_at": int(time.time()),
            "token_endpoint_auth_method": "none",
            "grant_types": ["authorization_code"],
            "response_types": ["code"],
            "redirect_uris": (body.get("redirect_uris") if isinstance(body, dict) else None) or [],
        }
    )


if __name__ == "__main__":
    log.info(
        "spawn-mcp starting on %s:%d; dns_rebinding_protection=%s allowed_hosts=%s allowed_origins=%s worktree=%s oauth_token_set=%s batch_enabled=%s",
        HOST,
        PORT,
        ENABLE_DNS_REBINDING_PROTECTION,
        ALLOWED_HOSTS,
        ALLOWED_ORIGINS,
        WORKTREE,
        bool(CLAUDE_CODE_OAUTH_TOKEN),
        BATCH_ENABLED,
    )
    mcp.run(transport="streamable-http")
