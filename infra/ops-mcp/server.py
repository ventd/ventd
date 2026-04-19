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

import asyncio
import json
import logging
import os
import re
import secrets
import subprocess
import time
import urllib.parse
from datetime import datetime, timezone
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

# Configurable paths (overridable for tests / non-standard installs)
REPO_ROOT = Path(os.environ.get("OPS_MCP_REPO_ROOT", "/opt/ops-mcp"))
SPAWN_MCP_LOG_DIR = Path(os.environ.get("OPS_MCP_SPAWN_LOG_DIR", "/var/log/spawn-mcp"))
SPAWN_MCP_TMPFS_DIR = Path(os.environ.get("OPS_MCP_SPAWN_TMPFS_DIR", "/tmp/spawn-mcp"))
GHRUNNER_DIAG_DIR = Path(
    os.environ.get("OPS_MCP_GHRUNNER_DIAG_DIR", "/opt/ghrunner/_work/_diag")
)

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
    "cowork-mcp",
    "cowork-mcp-tunnel",
    "ventd",
    "ventd-recover",
}

ALLOWED_SERVICE_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"^actions\.runner\.ventd-ventd\..*\.service$"),
]

ALLOWED_BINARY_PREFIXES: tuple[str, ...] = (
    "/usr/local/bin/",
    "/opt/",
    "/tmp/",
)

ALLOWED_PROFILE_PREFIXES: tuple[str, ...] = (
    "/etc/apparmor.d/",
    "/tmp/",
)

_TRYCLOUDFLARE_RE = re.compile(r"https://[a-zA-Z0-9-]+\.trycloudflare\.com")
_APPARMOR_DENIED_RE = re.compile(
    r"\[(?P<ts>[^\]]+)\].*apparmor=\"DENIED\"\s+"
    r"operation=\"(?P<op>[^\"]+)\"\s+"
    r"profile=\"(?P<profile>[^\"]+)\"\s+"
    r"name=\"(?P<name>[^\"]+)\"\s+"
    r"pid=(?P<pid>\d+)"
)


def _is_allowlisted(service: str) -> bool:
    if service in ALLOWED_SERVICES:
        return True
    for pat in ALLOWED_SERVICE_PATTERNS:
        if pat.match(service):
            return True
    return False


def _is_allowlisted_binary(path: str) -> bool:
    return any(path.startswith(p) for p in ALLOWED_BINARY_PREFIXES)


def _is_allowlisted_profile(path: str) -> bool:
    return any(path.startswith(p) for p in ALLOWED_PROFILE_PREFIXES)


def _is_sentinel_temp(millideg: int) -> bool:
    """Reject implausible temperature: >150 °C (covers 255.5 °C sentinel)."""
    return millideg > 150_000


def _is_sentinel_rpm(rpm: int) -> bool:
    """Reject 0xFFFF nct6687 sentinel or implausible RPM."""
    return rpm == 65535 or rpm > 10_000


def _is_sentinel_voltage(mv: int) -> bool:
    """Reject voltage above 20 V (covers 0xFFFF = 65.535 V sentinel)."""
    return mv > 20_000


def _parse_since(since: str) -> float:
    """Return Unix timestamp for a human-readable 'since' string."""
    s = since.strip().lower()
    now = time.time()
    m = re.match(r"^(\d+)\s+(second|minute|hour|day)s?\s+ago$", s)
    if m:
        n = int(m.group(1))
        unit = m.group(2)
        mul = {"second": 1, "minute": 60, "hour": 3600, "day": 86400}[unit]
        return now - n * mul
    try:
        dt = datetime.fromisoformat(since.replace("Z", "+00:00"))
        return dt.timestamp()
    except ValueError:
        pass
    return now - 3600


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


async def _run(
    *cmd: str,
    timeout: float = 30.0,
    input_data: bytes | None = None,
) -> tuple[bytes, bytes, int]:
    """Async subprocess helper. Returns (stdout, stderr, returncode)."""
    proc = await asyncio.create_subprocess_exec(
        *cmd,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
        stdin=asyncio.subprocess.PIPE if input_data is not None else None,
    )
    stdout, stderr = await asyncio.wait_for(
        proc.communicate(input=input_data), timeout=timeout
    )
    return stdout, stderr, proc.returncode or 0


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


# --- Group 1: systemd (new) --------------------------------------------------


@mcp.tool()
async def systemctl_status_fixed(unit: str) -> dict[str, Any]:
    """Structured service status including enabled state and recent log lines.

    Replaces the legacy text-scraping systemctl_status with structured
    systemctl show output. Returns active, enabled, sub_state, main_pid,
    and the last 10 log lines.
    """
    if not _is_allowlisted(unit):
        _audit({"kind": "reject", "tool": "systemctl_status_fixed", "unit": unit})
        raise ValueError(f"service not in allowlist: {unit}")

    show_out, _, _ = await _run(
        "sudo", "-n", "/bin/systemctl", "show", unit,
        "--property=ActiveState,SubState,MainPID,UnitFileState,LoadState",
        timeout=10.0,
    )
    parsed = _parse_systemd_show(show_out.decode())

    log_out, _, _ = await _run(
        "sudo", "-n", "/bin/journalctl",
        "-u", unit, "-n", "10", "--no-pager", "--output=short-iso",
        timeout=10.0,
    )

    _audit({"kind": "systemctl_status_fixed", "unit": unit})
    return {
        "active": parsed.get("ActiveState") == "active",
        "enabled": parsed.get("UnitFileState") in ("enabled", "enabled-runtime"),
        "sub_state": parsed.get("SubState", ""),
        "main_pid": int(parsed.get("MainPID") or 0),
        "last_log_lines": log_out.decode().splitlines()[-10:],
    }


@mcp.tool()
async def systemctl_restart_scoped(unit: str) -> dict[str, Any]:
    """Restart an allowlisted service, returning before/after PIDs and restart count.

    This is the structured alias for systemctl_restart with richer return data.
    """
    if not _is_allowlisted(unit):
        _audit({"kind": "reject", "tool": "systemctl_restart_scoped", "unit": unit})
        raise ValueError(f"service not in allowlist: {unit}")

    show_cmd = (
        "sudo", "-n", "/bin/systemctl", "show", unit,
        "--property=MainPID,NRestarts",
    )
    before_out, _, _ = await _run(*show_cmd, timeout=10.0)
    before = _parse_systemd_show(before_out.decode())
    before_pid = int(before.get("MainPID") or 0)

    await _run("sudo", "-n", "/bin/systemctl", "restart", unit, timeout=30.0)
    await asyncio.sleep(0.5)

    after_out, _, _ = await _run(*show_cmd, timeout=10.0)
    after = _parse_systemd_show(after_out.decode())

    _audit({"kind": "systemctl_restart_scoped", "unit": unit})
    return {
        "before_pid": before_pid,
        "after_pid": int(after.get("MainPID") or 0),
        "restart_count": int(after.get("NRestarts") or 0),
    }


# --- Group 2: diagnostics ----------------------------------------------------


@mcp.tool()
async def tunnel_current_url(service: str) -> dict[str, Any]:
    """Return the current trycloudflare tunnel URL for a service.

    Reads /etc/<service>/last-tunnel-url if present; otherwise parses the last
    5 lines of the <service>-tunnel journal for a trycloudflare.com URL.
    """
    if not _is_allowlisted(service):
        _audit({"kind": "reject", "tool": "tunnel_current_url", "service": service})
        raise ValueError(f"service not in allowlist: {service}")

    url_file = Path(f"/etc/{service}/last-tunnel-url")
    try:
        if url_file.exists():
            url = url_file.read_text().strip()
            mtime = url_file.stat().st_mtime
            rotated_at = datetime.fromtimestamp(mtime, tz=timezone.utc).isoformat()
            _audit({"kind": "tunnel_current_url", "service": service, "source": "file"})
            return {"url": url, "rotated_at": rotated_at, "source": "file"}
    except OSError:
        pass

    tunnel_unit = f"{service}-tunnel"
    if not _is_allowlisted(tunnel_unit):
        tunnel_unit = service
    jout, _, _ = await _run(
        "sudo", "-n", "/bin/journalctl",
        "-u", tunnel_unit, "-n", "5", "--no-pager", "--output=short-iso",
        timeout=10.0,
    )
    for line in reversed(jout.decode().splitlines()):
        m = _TRYCLOUDFLARE_RE.search(line)
        if m:
            _audit({
                "kind": "tunnel_current_url",
                "service": service,
                "source": "journal",
            })
            return {"url": m.group(0), "rotated_at": None, "source": "journal"}

    _audit({"kind": "tunnel_current_url", "service": service, "source": "none"})
    return {"url": None, "rotated_at": None, "source": "none"}


@mcp.tool()
async def runner_health() -> dict[str, Any]:
    """Return GitHub Actions runner health for the ventd-ventd pool.

    Queries systemctl for actions.runner.ventd-ventd.*.service units and
    the runner diagnostics directory. Returns online state, current job,
    job count over the last 24 hours, and registered labels.
    """
    list_out, _, rc = await _run(
        "sudo", "-n", "/bin/systemctl",
        "list-units", "--type=service", "--all",
        "--no-pager", "--output=json",
        timeout=15.0,
    )
    if rc != 0:
        _audit({"kind": "runner_health", "online": False})
        return {"online": False, "reason": "no_runner_service"}

    try:
        units_raw = json.loads(list_out.decode())
    except json.JSONDecodeError:
        units_raw = []

    runner_pat = re.compile(r"^actions\.runner\.ventd-ventd\..*\.service$")
    runner_units = [u for u in units_raw if runner_pat.match(u.get("unit", ""))]

    if not runner_units:
        _audit({"kind": "runner_health", "online": False})
        return {"online": False, "reason": "no_runner_service"}

    online = any(u.get("sub") == "running" for u in runner_units)
    current_job: str | None = None
    for u in runner_units:
        if u.get("sub") == "running":
            current_job = u.get("unit", "")
            break

    jobs_last_24h = 0
    cutoff = time.time() - 86400
    if GHRUNNER_DIAG_DIR.exists():
        for p in GHRUNNER_DIAG_DIR.glob("Worker_*.log"):
            try:
                if p.stat().st_mtime > cutoff:
                    jobs_last_24h += 1
            except OSError:
                pass

    labels: list[str] = []
    try:
        data = json.loads(Path("/opt/ghrunner/.runner").read_text())
        labels = [lb.get("name", "") for lb in data.get("labels", [])]
    except (json.JSONDecodeError, OSError):
        pass

    _audit({"kind": "runner_health", "online": online})
    return {
        "online": online,
        "current_job": current_job,
        "jobs_last_24h": jobs_last_24h,
        "labels": labels,
    }


@mcp.tool()
async def journal_grep_ventd(
    since: str, pattern: str, lines: int = 100
) -> dict[str, Any]:
    """Grep ventd journal logs for a pattern.

    Args:
        since: time expression, e.g. "1 hour ago" or ISO timestamp.
        pattern: regex pattern to match (applied in Python, not shell grep).
        lines: maximum number of matching lines to return (default 100).
    """
    if not _is_allowlisted("ventd"):
        _audit({"kind": "reject", "tool": "journal_grep_ventd"})
        raise ValueError("ventd not in allowlist")

    try:
        compiled = re.compile(pattern)
    except re.error as exc:
        raise ValueError(f"invalid pattern: {exc}") from exc

    lines = max(1, min(lines, 5000))
    jout, _, _ = await _run(
        "sudo", "-n", "/bin/journalctl",
        "-u", "ventd",
        f"--since={since}",
        "-n", str(lines * 10),
        "--no-pager",
        "--output=short-iso",
        timeout=30.0,
    )
    matches = [l for l in jout.decode().splitlines() if compiled.search(l)][:lines]
    _audit({"kind": "journal_grep_ventd", "since": since, "matches": len(matches)})
    return {"matches": matches}


@mcp.tool()
async def dmesg_grep_apparmor(since: str = "1 hour ago") -> dict[str, Any]:
    """Return structured AppArmor denial records from dmesg.

    Args:
        since: only include denials after this time (e.g. "1 hour ago").
    """
    cutoff = _parse_since(since)
    dout, _, _ = await _run("sudo", "-n", "/bin/dmesg", "-T", timeout=15.0)

    denials: list[dict[str, Any]] = []
    for line in dout.decode().splitlines():
        m = _APPARMOR_DENIED_RE.search(line)
        if not m:
            continue
        # Parse timestamp from dmesg -T: "[Mon Jan  1 00:00:00 2024]"
        ts_str = m.group("ts").strip()
        try:
            ts = datetime.strptime(ts_str, "%a %b %d %H:%M:%S %Y")
            ts = ts.replace(tzinfo=timezone.utc)
            if ts.timestamp() < cutoff:
                continue
        except ValueError:
            pass
        denials.append({
            "profile": m.group("profile"),
            "pid": int(m.group("pid")),
            "path": m.group("name"),
            "timestamp": ts_str,
        })

    _audit({"kind": "dmesg_grep_apparmor", "since": since, "count": len(denials)})
    return {"denials": denials}


@mcp.tool()
def hwmon_snapshot() -> dict[str, Any]:
    """Return a snapshot of all hwmon chip readings, sentinel-filtered.

    Walks /sys/class/hwmon/hwmon*/ and reads temp*_input, fan*_input,
    pwm*, and in*_input files. Applies ventd's sentinel-value filter.
    """
    hwmon_base = Path("/sys/class/hwmon")
    chips: list[dict[str, Any]] = []

    if not hwmon_base.exists():
        _audit({"kind": "hwmon_snapshot", "chips": 0})
        return {"chips": []}

    for chip_link in sorted(hwmon_base.iterdir()):
        chip_path = chip_link.resolve()
        try:
            name = (chip_path / "name").read_text().strip()
        except OSError:
            name = chip_link.name

        readings: dict[str, Any] = {}

        for sensor_file in sorted(chip_path.iterdir()):
            fname = sensor_file.name
            if not (
                fname.startswith("temp") and fname.endswith("_input")
                or fname.startswith("fan") and fname.endswith("_input")
                or fname.startswith("pwm") and not "_" in fname[3:]
                or fname.startswith("in") and fname.endswith("_input")
            ):
                continue
            try:
                raw = int(sensor_file.read_text().strip())
            except (OSError, ValueError):
                continue

            if fname.startswith("temp") and _is_sentinel_temp(raw):
                continue
            if fname.startswith("fan") and _is_sentinel_rpm(raw):
                continue
            if fname.startswith("in") and _is_sentinel_voltage(raw):
                continue

            readings[fname] = raw

        chips.append({"path": str(chip_path), "name": name, "readings": readings})

    _audit({"kind": "hwmon_snapshot", "chips": len(chips)})
    return {"chips": chips}


@mcp.tool()
async def apparmor_profile_validate(profile_path: str) -> dict[str, Any]:
    """Validate an AppArmor profile without loading it.

    Args:
        profile_path: path to the profile (must be under /etc/apparmor.d/ or /tmp/).

    Returns parses (bool), warnings and errors lists from apparmor_parser.
    """
    if not _is_allowlisted_profile(profile_path):
        _audit({
            "kind": "reject",
            "tool": "apparmor_profile_validate",
            "profile_path": profile_path,
        })
        raise ValueError(f"profile path not in allowlist: {profile_path}")

    if not Path(profile_path).exists():
        raise ValueError(f"profile not found: {profile_path}")

    _, stderr, rc = await _run(
        "sudo", "-n", "/usr/sbin/apparmor_parser", "--Qfile", profile_path,
        timeout=15.0,
    )
    err_text = stderr.decode()
    warnings = [l for l in err_text.splitlines() if "warning" in l.lower()]
    errors = [l for l in err_text.splitlines() if "error" in l.lower()]

    _audit({
        "kind": "apparmor_profile_validate",
        "profile_path": profile_path,
        "rc": rc,
    })
    return {
        "parses": rc == 0,
        "warnings": warnings,
        "errors": errors,
    }


@mcp.tool()
def install_script_dry_run(distro: str = "ubuntu-24.04") -> dict[str, Any]:
    """Parse scripts/install.sh and report what commands would run, without executing.

    Args:
        distro: target distro hint (e.g. 'ubuntu-24.04', 'fedora-40').
    """
    script_path = REPO_ROOT.parent.parent / "scripts" / "install.sh"
    if not script_path.exists():
        # Fall back to searching from known locations
        for candidate in [
            Path("/opt/ops-mcp/../../scripts/install.sh"),
            Path("/home/phoenix/ventd/scripts/install.sh"),
        ]:
            try:
                resolved = candidate.resolve()
                if resolved.exists():
                    script_path = resolved
                    break
            except OSError:
                continue

    actions: list[str] = []
    warnings: list[str] = []
    errors: list[str] = []

    if not script_path.exists():
        errors.append(f"install.sh not found (searched from {REPO_ROOT})")
        _audit({"kind": "install_script_dry_run", "distro": distro, "found": False})
        return {"actions": actions, "warnings": warnings, "errors": errors}

    cmd_re = re.compile(
        r"^\s*(apt-get|apt|dnf|yum|pacman|zypper|apk|pip3?|curl|wget|"
        r"systemctl|useradd|install|mkdir|cp|mv|chmod|chown|ln|rm)\s+(.+)"
    )
    distro_block_re = re.compile(r"#.*distro.*:?\s*(\S+)", re.IGNORECASE)

    current_distro_match = True
    for lineno, line in enumerate(script_path.read_text().splitlines(), 1):
        stripped = line.strip()
        if stripped.startswith("#"):
            dm = distro_block_re.search(stripped)
            if dm:
                current_distro_match = distro.startswith(dm.group(1))
            continue
        m = cmd_re.match(stripped)
        if m:
            tag = "" if current_distro_match else f" [skip:{distro}]"
            actions.append(f"L{lineno}: {stripped}{tag}")
        if "eval" in stripped or "exec " in stripped:
            warnings.append(f"L{lineno}: dynamic execution: {stripped[:80]}")

    _audit({"kind": "install_script_dry_run", "distro": distro, "actions": len(actions)})
    return {"actions": actions, "warnings": warnings, "errors": errors}


@mcp.tool()
async def binary_size_measure(binary_path: str) -> dict[str, Any]:
    """Measure binary size and ELF section breakdown.

    Args:
        binary_path: path to binary (must be under /usr/local/bin/, /opt/, or /tmp/).

    Returns bytes (total file size) and sections (.text, .data, .rodata).
    """
    if not _is_allowlisted_binary(binary_path):
        _audit({
            "kind": "reject",
            "tool": "binary_size_measure",
            "binary_path": binary_path,
        })
        raise ValueError(f"binary path not in allowlist: {binary_path}")

    if not Path(binary_path).exists():
        raise ValueError(f"binary not found: {binary_path}")

    total_bytes = Path(binary_path).stat().st_size

    size_out, _, rc = await _run("/usr/bin/size", "-A", binary_path, timeout=10.0)
    sections: dict[str, int] = {}
    if rc == 0:
        for line in size_out.decode().splitlines():
            parts = line.split()
            if len(parts) >= 2 and parts[0] in (".text", ".data", ".rodata"):
                try:
                    sections[parts[0]] = int(parts[1])
                except ValueError:
                    pass

    _audit({
        "kind": "binary_size_measure",
        "binary_path": binary_path,
        "bytes": total_bytes,
    })
    return {"bytes": total_bytes, "sections": sections}


@mcp.tool()
def disk_free(path: str = "/") -> dict[str, Any]:
    """Return disk usage for a filesystem path.

    Args:
        path: filesystem path to check (default: /).
    """
    try:
        st = os.statvfs(path)
    except OSError as exc:
        raise ValueError(f"statvfs failed for {path}: {exc}") from exc

    block = st.f_frsize or st.f_bsize
    total = st.f_blocks * block
    free = st.f_bavail * block
    used = total - st.f_bfree * block

    _audit({"kind": "disk_free", "path": path})
    return {
        "total_bytes": total,
        "used_bytes": used,
        "free_bytes": free,
        "mount_point": path,
    }


@mcp.tool()
def cc_log_find(session_name: str) -> dict[str, Any]:
    """Find and summarise a spawn-mcp session log.

    Args:
        session_name: session identifier (filename without .log extension).
    """
    # Sanitise: session_name must not contain path separators
    if "/" in session_name or "\\" in session_name or session_name.startswith("."):
        raise ValueError(f"invalid session name: {session_name}")

    log_path = SPAWN_MCP_LOG_DIR / "sessions" / f"{session_name}.log"
    if not log_path.exists():
        _audit({"kind": "cc_log_find", "session": session_name, "found": False})
        return {
            "log_path": str(log_path),
            "size_bytes": None,
            "started_at": None,
            "ended_at": None,
            "exit_code": None,
        }

    stat_result = log_path.stat()
    size_bytes = stat_result.st_size
    started_at = datetime.fromtimestamp(
        stat_result.st_ctime, tz=timezone.utc
    ).isoformat()
    ended_at = datetime.fromtimestamp(
        stat_result.st_mtime, tz=timezone.utc
    ).isoformat()

    exit_code: int | None = None
    exit_re = re.compile(r"spawn-mcp:\s+claude\s+exited\s+rc=(\d+)")
    try:
        for line in log_path.read_text(errors="replace").splitlines():
            m = exit_re.search(line)
            if m:
                exit_code = int(m.group(1))
    except OSError:
        pass

    _audit({"kind": "cc_log_find", "session": session_name, "found": True})
    return {
        "log_path": str(log_path),
        "size_bytes": size_bytes,
        "started_at": started_at,
        "ended_at": ended_at,
        "exit_code": exit_code,
    }


@mcp.tool()
def cc_audit_log(
    since: str = "1 hour ago", kind_filter: list[str] | None = None
) -> dict[str, Any]:
    """Return spawn-mcp audit log events, filtered by time and optional kind.

    Args:
        since: time expression, e.g. "1 hour ago" or ISO timestamp.
        kind_filter: if provided, only return events matching these kinds.
    """
    audit_path = SPAWN_MCP_LOG_DIR / "audit.jsonl"
    cutoff = _parse_since(since)
    events: list[dict[str, Any]] = []

    if not audit_path.exists():
        _audit({"kind": "cc_audit_log", "found": False})
        return {"events": []}

    try:
        for line in audit_path.read_text(errors="replace").splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                entry = json.loads(line)
            except json.JSONDecodeError:
                continue
            ts_str = entry.get("ts", "")
            try:
                ts = datetime.fromisoformat(ts_str.replace("Z", "+00:00")).timestamp()
                if ts < cutoff:
                    continue
            except ValueError:
                pass
            if kind_filter and entry.get("kind") not in kind_filter:
                continue
            events.append(entry)
    except OSError:
        pass

    _audit({"kind": "cc_audit_log", "since": since, "events": len(events)})
    return {"events": events}


@mcp.tool()
async def process_tree(pid: int) -> dict[str, Any]:
    """Return a process tree rooted at the given PID.

    Args:
        pid: root PID for the tree. Use 1 for the full init tree.
    """
    out, _, _ = await _run(
        "/bin/ps", "-eo", "pid,ppid,stat,cmd", "--no-headers",
        timeout=10.0,
    )
    by_pid: dict[int, dict[str, Any]] = {}
    children: dict[int, list[int]] = {}

    for line in out.decode().splitlines():
        parts = line.split(None, 3)
        if len(parts) < 4:
            continue
        try:
            p = int(parts[0])
            pp = int(parts[1])
        except ValueError:
            continue
        by_pid[p] = {
            "pid": p,
            "ppid": pp,
            "cmd": parts[3],
            "zombie": "Z" in parts[2],
            "children": [],
        }
        children.setdefault(pp, []).append(p)

    def _build(p: int) -> dict[str, Any] | None:
        node = by_pid.get(p)
        if node is None:
            return None
        node["children"] = [
            c for c in (
                _build(child) for child in children.get(p, [])
            ) if c is not None
        ]
        return node

    tree = _build(pid)
    if tree is None:
        raise ValueError(f"pid {pid} not found")

    _audit({"kind": "process_tree", "pid": pid})
    return tree


# --- Group 3: incus + cleanup ------------------------------------------------


@mcp.tool()
async def incus_smoke_spawn(
    distro: str,
    script_url: str,
    timeout_sec: int = 300,
) -> dict[str, Any]:
    """Launch a disposable Incus container, run a bootstrap script, and destroy it.

    Args:
        distro: image name suffix (e.g. 'ubuntu/24.04', 'debian/12').
        script_url: HTTPS URL to a bootstrap script (curl-piped to bash).
        timeout_sec: max seconds for the bootstrap script (default 300).

    The container is named smoke-<random> and auto-destroyed on completion.
    """
    if not script_url.startswith("https://"):
        raise ValueError("script_url must start with https://")

    container_id = f"smoke-{secrets.token_hex(6)}"
    stdout_tail: list[str] = []
    stderr_tail: list[str] = []
    exit_code = -1

    try:
        _, err, rc = await _run(
            "/usr/bin/incus", "launch", f"images:{distro}", container_id,
            timeout=60.0,
        )
        if rc != 0:
            raise RuntimeError(f"incus launch failed: {err.decode()[:500]}")

        await asyncio.sleep(3)

        out, err, exit_code = await _run(
            "/usr/bin/incus", "exec", container_id, "--",
            "/bin/bash", "-c", f"curl -sf {script_url} | bash",
            timeout=float(timeout_sec),
        )
        stdout_tail = out.decode(errors="replace").splitlines()[-50:]
        stderr_tail = err.decode(errors="replace").splitlines()[-20:]
    finally:
        try:
            await _run(
                "/usr/bin/incus", "delete", "--force", container_id, timeout=30.0
            )
        except Exception:
            log.warning("failed to destroy container %s", container_id)

    _audit({
        "kind": "incus_smoke_spawn",
        "distro": distro,
        "container_id": container_id,
        "exit_code": exit_code,
    })
    return {
        "container_id": container_id,
        "exit_code": exit_code,
        "stdout_tail": stdout_tail,
        "stderr_tail": stderr_tail,
    }


@mcp.tool()
async def incus_smoke_cleanup(container_id: str) -> dict[str, Any]:
    """Destroy a stranded smoke container by ID.

    Args:
        container_id: container name, must start with 'smoke-'.
    """
    if not container_id.startswith("smoke-"):
        _audit({
            "kind": "reject",
            "tool": "incus_smoke_cleanup",
            "container_id": container_id,
        })
        raise ValueError("container_id must start with 'smoke-'")

    _, _, rc = await _run(
        "/usr/bin/incus", "delete", "--force", container_id, timeout=30.0
    )
    _audit({
        "kind": "incus_smoke_cleanup",
        "container_id": container_id,
        "destroyed": rc == 0,
    })
    return {"destroyed": rc == 0}


@mcp.tool()
def tmpfs_clear_cc_prompts(age_min: int = 60) -> dict[str, Any]:
    """Delete spawn-mcp prompt files older than age_min minutes from /tmp/spawn-mcp/.

    Args:
        age_min: minimum age in minutes before a file is eligible for removal (default 60).
    """
    age_min = max(1, age_min)
    cutoff = time.time() - age_min * 60
    removed: list[str] = []
    kept: list[str] = []

    if not SPAWN_MCP_TMPFS_DIR.exists():
        _audit({"kind": "tmpfs_clear_cc_prompts", "removed": 0, "kept": 0})
        return {"removed_files": [], "kept_files": []}

    for p in SPAWN_MCP_TMPFS_DIR.iterdir():
        if not p.is_file():
            continue
        try:
            mtime = p.stat().st_mtime
        except OSError:
            continue
        if mtime < cutoff:
            try:
                p.unlink()
                removed.append(str(p))
            except OSError:
                kept.append(str(p))
        else:
            kept.append(str(p))

    _audit({
        "kind": "tmpfs_clear_cc_prompts",
        "removed": len(removed),
        "kept": len(kept),
    })
    return {"removed_files": removed, "kept_files": kept}


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
