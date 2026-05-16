# ventd — Claude Code Guidance

## Project
Linux-first automatic fan controller daemon. Go 1.25+, CGO_ENABLED=0
(purego dlopen for NVML), GPL-3.0. Runs as systemd unit. Hwmon/sysfs only.

## Build / Test / Run
- `make build`        # goreleaser snapshot binary
- `make test`         # full suite with race detector
- `make cover`        # per-package coverage
- `make lint`         # golangci-lint
- `make safety-run`   # hwmon-safety invariant subtests
- `make e2e`          # fresh-VM smoke suite (requires vagrant)
- `make pre-push`     # mirrors scripts/ci-local.sh — run before push
- `make sbom`         # CycloneDX + SPDX SBOMs via goreleaser+syft
- `make verify-repro` # reproducibility smoke test
- `make sync-embeds`  # refresh install.sh + CHANGELOG embeds after edits

## Rule catalogs
Every safety invariant is RULE-<FAMILY>-NN bound 1:1 to a subtest;
`tools/rulelint` enforces the pairing at CI time. The index lives at
`.claude/RULE-INDEX.md` and the per-family files in `.claude/rules/`.
Use the ventd-rulelint skill to enforce; open specific rule files on demand.

## Invariants without a RULE- yet
- CGO_ENABLED=0 — purego dlopen only.
- Wrap errors: `fmt.Errorf("read %s: %w", path, err)`.
- `errors.Is/As` for control flow — never string-match.
- Every goroutine tied to a `context.Context`.
- Sender closes channels.
- slog JSON handler; journald reads stdout/stderr.
- `sd_notify READY=1` only after config validated AND first PWM write OK.
- No panics in the control loop — recover, log, degrade safely.
- Table-driven tests; hermetic, no real `/sys`. Mock sysfs via
  `testing/fstest` or `fs.FS`.

## Don't
- No cgo deps. Use purego.
- No stdlib `log` — use slog.
- No `os.Exit` outside main.
- No `context.Context` stored in structs.
- No real `/sys` in unit tests.
- README never promises what isn't shipped in a tagged release.

## Compact instructions
On `/compact`: preserve test failures, rule violations, code changes,
pending TODOs, unresolved design questions. Drop raw tool output, full
file reads, exploratory greps, passing test lines.
