# Cross-distro install status

Human-owned record of which Linux distros and architectures the current
ventd release has been smoke-tested on. The cells are promoted by hand
after reviewing the per-run detail under
[docs/cross-distro-runs/](cross-distro-runs/).

Populated by `scripts/cross-distro-smoke.sh` — see
[cross-distro-smoke.md](cross-distro-smoke.md) for the harness and the
definition of a pass.

Legend:
- `—` — not yet tested at this version
- `PASS` / `FAIL` — last known result (tag noted in parentheses)
- `SKIP` — intentionally out of scope for this arch

| Distro               | amd64 | arm64 | Notes |
|----------------------|:-----:|:-----:|-------|
| Ubuntu 24.04         |   —   |   —   | |
| Debian 12            |   —   |   —   | |
| Fedora 40            |   —   |   —   | |
| Arch                 |   —   |   —   | |
| openSUSE Tumbleweed  |   —   |   —   | |
| Void (glibc)         |   —   |   —   | |
| Alpine 3.19          |   —   |   —   | |

Last updated: pending first run.
