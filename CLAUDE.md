# ventd — Claude Code Guidance

## Project
Linux-first automatic fan controller daemon. Go 1.25+, CGO_ENABLED=0 (purego
dlopen for NVML), GPL-3.0. Runs as systemd unit. Hwmon/sysfs only. Dev on
WSL2 at /home/phoenix/ventd. HIL at 192.168.7.222 (MiniPC) and Proxmox at
192.168.7.10.

## Build / Test / Run
- `make build`          # goreleaser snapshot binary
- `make test`           # full suite with race detector
- `make cover`          # per-package coverage
- `make lint`           # golangci-lint
- `make safety-run`     # hwmon-safety invariant subtests
- `make e2e`            # fresh-VM smoke suite (requires vagrant)
- `make sbom`           # CycloneDX + SPDX SBOMs via goreleaser+syft
- `make verify-repro`   # reproducibility smoke test

## Rule catalogs (1:1 rule ↔ subtest, enforced by tools/rulelint)
- RULE-HAL-*      HAL backend contract (Enumerate/Read/Write/Restore/Caps/Close)
- RULE-HWMON-*    Hardware safety (stop-gated, clamp, enable-mode, sentinels, sysfs ENOENT)
- RULE-WD-*       Watchdog safety (restore on exit, NVML reset, RPM target, idempotent)
- RULE-CAL-*      Calibration safety (zero-fires/cancel/rearm/stop, detect happy/concurrent)

See `.claude/RULE-INDEX.md` for the full rule map (use ventd-rulelint skill to enforce).

## Invariants that don't have a RULE- yet
- CGO_ENABLED=0 — no cgo deps; purego dlopen only
- Wrap errors: fmt.Errorf("read %s: %w", path, err)
- errors.Is/As for control flow — never string-match
- Every goroutine tied to a context.Context
- Sender closes channels
- slog JSON handler; journald reads stdout/stderr
- sd_notify READY=1 after config validated + first PWM write OK
- No panics in control loop — recover, log, degrade safely
- Table-driven tests; hermetic, no real /sys
- Mock sysfs via testing/fstest or fs.FS

## See also (loaded on demand)
- `@specs/` — feature specs (use ventd-specs skill)
- `.claude/RULE-INDEX.md` — rule map; open specific rule files on demand (use ventd-rulelint skill)

## Current roadmap
Phase 4 order: SLEEP → PI → HYST → LATCH+STEP → PI-autotune → HWCURVE →
INTERFERENCE → DITHER → MPC. Top priority: spec-01 IPMI polish → v0.3.x.
Next: v0.4.0 Corsair.

## Compact instructions
When using /compact, preserve: test failures, rule violations, code changes,
pending TODOs, unresolved design questions. Drop: raw tool output, full file
reads, exploratory greps, passing test lines.

## Don't
- No cgo deps. Use purego.
- No stdlib `log` — use slog.
- No os.Exit outside main.
- No context.Context stored in structs.
- No real /sys in unit tests.
- README never promises what isn't shipped in a tagged release.
- Phoenix-only actions (per .claude/rules/collaboration.md): commits, merges,
  pushes, issue creation. Claude drafts; Phoenix executes.

## Budget reality
- Haiku for mechanical work (tests, commits, lint fixes)
- Sonnet for implementation
- Opus ONLY in claude.ai chat (never in CC)
- No multi-agent spawning (hook blocks >3/session)
