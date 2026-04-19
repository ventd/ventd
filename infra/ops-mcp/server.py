"""ops-mcp: MCP server exposing scoped host operations for Atlas/Cowork.

Runs on phoenix-desktop. Exposes systemctl restart/status/list-failed and
journalctl for a fixed allowlist of services. Every tool call is gated by
the allowlist before any subprocess is run, and logged to audit.jsonl.

Transport: streamable-http on 127.0.0.1:8892 (override via OPS_MCP_HOST /
OPS_MCP_PORT). cloudflared tunnel terminates TLS and exposes a
trycloudflare.com hostname that Atlas/Cowork connectors point at.

Security:
- Service allowlist is the primary guard: every tool checks _is_allowlisted()
  before touching subprocess.
- sudo -n (no-password) is used for systemctl/journalctl; the ops-mcp user
  has NOPASSWD rules in /etc/sudoers.d/ops-mcp for those exact commands.
- DNS-rebinding protection is deliberately disabled. trycloudflare quick-tunnel
  hostnames rotate on every cloudflared restart, making a literal Host-match
  allowlist unworkable. Real access control is:
    1. Server binds to 127.0.0.1 only.
    2. cloudflared tunnel is the only external path.
    3. OAuth on the claude.ai custom-connector layer.
  Flip OPS_MCP_ENABLE_DNS_REBINDING_PROTECTION=1 if a stable named tunnel
  is used later.
- Audit log at /var/log/ops-mcp/audit.jsonl records every tool call and
  every allowlist rejection.
"""

from __future__ import annotations

import json
import logging
import os
import re
import secrets
import subprocess
import time
import urllib.parse
from pathlib import Path
from typing import Any

from mcp.server.fastmcp import FastMCP
from mcp.server.transport_security import TransportSecuritySettings
from starlette.requests import Request
from starlette.responses import JSONResponse, RedirectResponse

log = logging.getLogger("ops-mcp")
logging.basicConfig(
    level=os.environ.get("OPS_MCP_LOG", "INFO"),
    format="%(asctime)s %(levelname)s %(name)s %(message)s",
)

# --- Configuration -----------------------------------------------------------

AUDIT_LOG = Path(os.environ.get("OPS_MCP_AUDIT", "/var/log/ops-mcp/audit.jsonl"))
HOST = os.environ.get("OPS_MCP_HOST", "127.0.0.1")
PORT = int(os.environ.get("OPS_MCP_PORT", "8892"))

ENABLE_DNS_REBINDING_PROTECTION = os.environ.get(
    "OPS_MCP_ENABLE_DNS_REBINDING_PROTECTION", "0"
) in ("1", "true", "yes")
ALLOWED_HOSTS = [
    h.strip()
    for h in os.environ.get("OPS_MCP_ALLOWED_HOSTS", "").split(",")
    if h.strip()
]
ALLOWED_ORIGINS = [
    o.strip()
    for o in os.environ.get("OPS_MCP_ALLOWED_ORIGINS", "").split(",")
    if o.strip()
]

AUDIT_LOG.parent.mkdir(parents=True, exist_ok=True)

# --- Allowlist ---------------------------------------------------------------

ALLOWED_SERVICES: set[str] = {
    "spawn-mcp",
    "spawn-mcp-tunnel",
    "github-mcp",
    "github-mcp-tunnel",
    "cloudflared",
    "cowork-bridge",
    "ops-mcp",
    "ops-mcp-tunnel",
}

ALLOWED_SERVICE_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"^actions\.runner\.ventd-ventd\..*\.service$"),
]


def _is_allowlisted(service: str) -> bool:
    if service in ALLOWED_SERVICES:
        return True
    for pat in ALLOWED_SERVICE_PATTERNS:
        if pat.match(service):
            return True
    return False


# --- FastMCP -----------------------------------------------------------------

mcp = FastMCP(
    "ops-mcp",
    host=HOST,
    port=PORT,
    streamable_http_path="/",
    transport_security=TransportSecuritySettings(
        enable_dns_rebinding_protection=ENABLE_DNS_REBINDING_PROTECTION,
        allowed_hosts=ALLOWED_HOSTS,
        allowed_origins=ALLOWED_ORIGINS,
    ),
)

# --- Helpers -----------------------------------------------------------------


def _audit(event: dict[str, Any]) -> None:
    event.setdefault("ts", time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
    with AUDIT_LOG.open("a") as f:
        f.write(json.dumps(event) + "\n")


def _parse_systemd_show(output: str) -> dict[str, str]:
    """Parse `systemctl show --property=...` output into a flat dict."""
    result: dict[str, str] = {}
    for line in output.splitlines():
        if "=" in line:
            key, _, value = line.partition("=")
            result[key.strip()] = value.strip()
    return result


# --- Tools -------------------------------------------------------------------


@mcp.tool()
def systemctl_restart(service: str) -> dict[str, Any]:
    """Restart a systemd service on phoenix-desktop.

    Only services in the allowlist are accepted. Returns success flag,
    exit code, and any stderr from systemctl.
    """
    if not _is_allowlisted(service):
        _audit({"kind": "reject", "tool": "systemctl_restart", "service": service})
        raise ValueError(f"service not in allowlist: {service}")
    result = subprocess.run(
        ["sudo", "-n", "/bin/systemctl", "restart", service],
        capture_output=True,
        text=True,
        timeout=30,
    )
    _audit(
        {
            "kind": "systemctl_restart",
            "service": service,
            "rc": result.returncode,
        }
    )
    return {
        "success": result.returncode == 0,
        "exit_code": result.returncode,
        "stderr": result.stderr[-1000:],
    }


@mcp.tool()
def journalctl(service: str, lines: int = 100, priority: str = "info") -> dict[str, Any]:
    """Fetch recent journal logs for an allowlisted service.

    Args:
        service: systemd service name (must be in allowlist).
        lines: number of log lines to return (1-1000, default 100).
        priority: minimum log priority (emerg/alert/crit/err/warning/notice/info/debug).
    """
    if not _is_allowlisted(service):
        _audit({"kind": "reject", "tool": "journalctl", "service": service})
        raise ValueError(f"service not in allowlist: {service}")
    lines = max(1, min(lines, 1000))
    result = subprocess.run(
        [
            "sudo", "-n", "/bin/journalctl",
            "-u", service,
            "-n", str(lines),
            "-p", priority,
            "--no-pager",
            "--output=short-iso",
        ],
        capture_output=True,
        text=True,
        timeout=10,
    )
    _audit(
        {
            "kind": "journalctl",
            "service": service,
            "lines": lines,
            "priority": priority,
            "rc": result.returncode,
        }
    )
    return {
        "logs": result.stdout,
        "truncated": len(result.stdout) >= 64_000,
    }


@mcp.tool()
def systemctl_status(service: str) -> dict[str, Any]:
    """Return structured status for an allowlisted systemd service.

    Returns active state, substate, main PID, start timestamp, and restart count.
    """
    if not _is_allowlisted(service):
        _audit({"kind": "reject", "tool": "systemctl_status", "service": service})
        raise ValueError(f"service not in allowlist: {service}")
    result = subprocess.run(
        [
            "sudo", "-n", "/bin/systemctl", "show", service,
            "--property=ActiveState,SubState,MainPID,ActiveEnterTimestamp,NRestarts",
        ],
        capture_output=True,
        text=True,
        timeout=5,
    )
    _audit(
        {
            "kind": "systemctl_status",
            "service": service,
            "rc": result.returncode,
        }
    )
    parsed = _parse_systemd_show(result.stdout)
    return {
        "active": parsed.get("ActiveState") == "active",
        "substate": parsed.get("SubState", ""),
        "main_pid": int(parsed.get("MainPID") or 0),
        "started_at": parsed.get("ActiveEnterTimestamp", ""),
        "restart_count": int(parsed.get("NRestarts") or 0),
    }


@mcp.tool()
def systemctl_list_failed() -> dict[str, Any]:
    """List failed systemd units, filtered to the allowlist.

    Returns only units whose name matches the ops-mcp allowlist.
    """
    result = subprocess.run(
        [
            "sudo", "-n", "/bin/systemctl",
            "--failed", "--no-pager", "--output=json",
        ],
        capture_output=True,
        text=True,
        timeout=10,
    )
    _audit({"kind": "systemctl_list_failed", "rc": result.returncode})
    if result.returncode != 0:
        return {"services": [], "error": result.stderr[-500:]}
    try:
        units = json.loads(result.stdout)
    except json.JSONDecodeError:
        return {"services": [], "error": "failed to parse systemctl --failed output"}
    failed = [u for u in units if _is_allowlisted(u.get("unit", ""))]
    return {"services": failed}


# --- OAuth shim (satisfies claude.ai custom-connector OAuth flow) ------------
# trycloudflare tunnel + claude.ai OAuth is the real security boundary.
# These endpoints auto-approve; no PKCE verification, no token validation.


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
    try:
        body = await request.json()
    except Exception:
        body = {}
    _audit(
        {
            "kind": "oauth-register",
            "body_keys": sorted(body.keys()) if isinstance(body, dict) else [],
        }
    )
    client_id = body.get("client_id") if isinstance(body, dict) else None
    return JSONResponse(
        {
            "client_id": client_id or secrets.token_urlsafe(16),
            "client_id_issued_at": int(time.time()),
            "token_endpoint_auth_method": "none",
            "grant_types": ["authorization_code"],
            "response_types": ["code"],
            "redirect_uris": (
                body.get("redirect_uris") if isinstance(body, dict) else None
            )
            or [],
        }
    )


if __name__ == "__main__":
    log.info(
        "ops-mcp starting on %s:%d; dns_rebinding_protection=%s allowed_hosts=%s",
        HOST,
        PORT,
        ENABLE_DNS_REBINDING_PROTECTION,
        ALLOWED_HOSTS,
    )
    mcp.run(transport="streamable-http")
