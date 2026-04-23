# Commit conventions — ventd

## Format

```
<type>(<scope>): <subject>

<body>

<footer>
```

- Subject: imperative mood, ≤72 chars, lowercase first letter, no trailing period.
- Body: wrap at 72 cols. Explain *why*, not *what*. Optional for trivial commits.
- Footer: `Fixes #N`, `Refs #N`, `BREAKING CHANGE: <description>`. Each on its own line.

## Types

| Type       | When to use                                          | CHANGELOG section |
|------------|------------------------------------------------------|-------------------|
| `feat`     | New user-visible feature or capability               | Added             |
| `fix`      | Bug fix                                              | Fixed             |
| `refactor` | Code change with no behaviour change                 | Changed           |
| `perf`     | Performance improvement                              | Changed           |
| `test`     | Adding or fixing tests only                          | (none)            |
| `docs`     | Documentation only                                   | (none)            |
| `build`    | Build system, goreleaser, Makefile                   | (none)            |
| `ci`       | CI pipeline changes                                  | (none)            |
| `chore`    | Tooling, config, deps, gitignore                     | (none)            |
| `revert`   | Reverts a prior commit                               | (none)            |

BREAKING CHANGE in footer triggers a CHANGELOG "Changed" entry prefixed with **BREAKING:**.

## Scopes — ventd-specific

Scope is the `internal/<pkg>` leaf, or a known top-level identifier.
Omit scope for cross-cutting changes (affects ≥3 unrelated packages).

| Scope            | What it covers                                      |
|------------------|-----------------------------------------------------|
| `controller`     | PWM control loop (`internal/controller/`)           |
| `hal/hwmon`      | hwmon sysfs backend (`internal/hal/hwmon/`)         |
| `hal/ipmi`       | IPMI backend (`internal/hal/ipmi/`)                 |
| `hal/nvml`       | NVIDIA NVML backend (`internal/hal/nvml/`)          |
| `hal/usbbase`    | USB HID base layer (`internal/hal/usbbase/`)        |
| `calibrate`      | Calibration sweep (`internal/calibrate/`)           |
| `watchdog`       | Watchdog safety layer (`internal/watchdog/`)        |
| `monitor`        | Hardware monitor / scan (`internal/monitor/`)       |
| `config`         | Config loading and validation (`internal/config/`)  |
| `web/ui`         | Web UI HTML/JS (`internal/web/`)                    |
| `web/api`        | REST API handlers (`internal/web/`)                 |
| `authpersist`    | Auth token persistence (`internal/authpersist/`)    |
| `hwdb`           | Hardware fingerprint DB (`internal/hwdb/`)          |
| `testfixture`    | Shared test fixtures (`internal/testfixture/`)      |
| `cmd/ventd`      | Main binary entry point (`cmd/ventd/`)              |
| `deploy`         | systemd units, AppArmor, install scripts            |
| `ops-mcp`        | Ops MCP server                                      |
| `claude`         | `.claude/` config, skills, hooks, settings          |
| `ci`             | `.github/workflows/`                                |
| `build`          | `Makefile`, `goreleaser.yml`, `tools/`              |

## CHANGELOG rules

Edit `CHANGELOG.md` before running `git commit`. Target the `## [Unreleased]`
section. If it doesn't exist yet, create it at the top of the file.

```markdown
## [Unreleased]

### Added
- feat bullets here

### Fixed
- fix bullets here

### Changed
- refactor / perf / BREAKING bullets here
```

Write bullets in user-facing language (not "refactor the clamp path" —
"fan speed changes now always respect min_pwm and max_pwm limits").
`test`, `docs`, `build`, `ci`, `chore` commits do not touch CHANGELOG.

## Linear history

- No merge commits. Rebase before merging.
- Squash within a feature branch if individual commits are WIP noise;
  keep commits that represent green-test milestones.
- Never `git push --force` to `main`.
- Never `--no-verify`.

## Issue references

- `Fixes #N` — closes the issue when the PR merges. Use when the commit
  fully resolves the issue.
- `Refs #N` — links without closing. Use for partial fixes or related
  work.
- Place in the commit footer, one per line.

## Forbidden trailers

The following must NEVER appear in any commit:

- `Co-Authored-By: Claude ...`
- `Co-Authored-By: Anthropic ...`
- Any mention of AI, LLM, agent, assistant, copilot in subject/body/footer.
- `🤖 Generated with Claude Code` or similar.

See `.claude/rules/attribution.md` for the full policy.

## Examples

**Feature commit:**
```
feat(calibrate): add hysteresis detection to RPM correlation sweep

The correlation window was too narrow for fans with thermal inertia,
causing DetectRPMSensor to return no-winner on slow-spinning PWM fans
at low ambient temperature.

Fixes #312
```

**Fix commit with BREAKING CHANGE:**
```
fix(hal/hwmon): reject in*_input reads above 20 V as sentinel

Voltage above 20 V after mV→V scaling is a 0xFFFF chip sentinel.
Previously the reading passed through and drove garbage PWM output
on voltage-bound curves.

BREAKING CHANGE: Callers relying on reads above 20 V must handle
IsSentinelSensorVal returning true for those values.

Fixes #460
```

**Chore commit (no CHANGELOG):**
```
chore(claude): track hooks, skills, and settings; ignore only local
```
