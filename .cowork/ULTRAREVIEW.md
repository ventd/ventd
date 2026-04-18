# Ultrareview — whole-repo audit gate

## Purpose

Per-PR review (masterplan §5 rows R1-R18, testplan §18 rows R19-R23) catches issues within a diff. It cannot catch:

- Architectural drift across PRs (three backends that silently diverge on `Restore` semantics).
- Dead code (PR A added a helper that PR B used, B got reverted, A's helper is now orphaned).
- Duplication across PRs (three backends each reimplement a tiny helper, nobody extracted it).
- Cumulative tech debt (10 PRs each add a `// TODO:` that nobody consolidates).
- Public-API dead exports.
- Cumulative binary size drift (each +80 KB passes R15; ten of them is +800 KB).
- CHANGELOG consistency across many edits.
- Config-schema validator gaps introduced by refactors.
- Docs drift (API routes in code vs docs/api.md).

Ultrareview is the periodic whole-repo audit that catches these.

## Trigger rules

Ultrareview runs when EITHER:

1. **10 PRs have merged** since the last ultrareview.
2. **A phase boundary closes** (last task in Phase N merged), regardless of PR count since last ultrareview.

When either trigger fires, Cowork HALTS new dispatches for the ultrareview task. No new CC sessions start until the ultrareview report is reviewed and any findings either addressed or filed as follow-up tasks.

Counter lives at `.cowork/ULTRAREVIEW.md` → `last_ultrareview_sha` + `last_ultrareview_pr_count`. On merge, Cowork increments `pr_count_since_last`; when it hits 10, trigger fires.

## What ultrareview checks

The CC dispatched for ultrareview runs through this catalogue. Each check produces a PASS / WARN / FAIL verdict with evidence. Findings go into a single report at `.cowork/reviews/ultrareview-<N>.md`.

### ULTRA-01 — HAL contract coherence
For every backend (hwmon, nvml, future IPMI/LIQUID/CROSEC/PWMSYS/ASAHI): does `Enumerate` / `Read` / `Write` / `Restore` / `Close` actually obey the contract in `.claude/rules/hal-contract.md`? In particular:
- Is `Restore` idempotent + safe on unopened channels?
- Does `Read` never write?
- Does `Write` always clamp to [MinPWM, MaxPWM]?
- Is `Caps` stable across `Enumerate` calls?

Method: diff the five methods' implementations across backends, look for divergent error handling, divergent logging, divergent mode-transition semantics.

### ULTRA-02 — Safety posture coherence
- Every goroutine spawned by production code has a documented lifecycle (ctx or stop channel) — grep for `go func` and cross-ref.
- Every `defer wd.Restore()` path is still in place; no PR accidentally removed one.
- No `log.Fatal` / `os.Exit` / `panic()` added outside `cmd/*/main.go`.
- Every file read/write in production code has a `defer Close()` or justification.

### ULTRA-03 — Rule file integrity
- Every rule in every `.claude/rules/*.md` file still has a bound subtest (rulelint catches this per-PR but races are possible).
- No two rules contradict each other (e.g. "always X" + "never X under condition Y" with overlapping scope).
- Rules file word count / rule count reasonable — if a rule file has grown beyond 20 rules, it probably needs splitting.

### ULTRA-04 — Dead code
- Run `deadcode ./...` (golang.org/x/tools/cmd/deadcode). Report all unreachable functions.
- Identify exported identifiers in `internal/` not referenced from any `cmd/` or test.
- Identify config fields defined in YAML schema but never read in code.

### ULTRA-05 — Duplication audit
- Run `dupl -t 50 ./...` or equivalent. Report any duplicated 50-line block.
- Manually check the five HAL backends for helper duplication that should live in `internal/hal/common/`.

### ULTRA-06 — Test coverage map
- `go test -coverprofile=cover.out ./...` + `go tool cover -func=cover.out`.
- Per-package coverage table. Flag any package below 80% with a note.
- Compare to last ultrareview's coverage (if present) — flag regressions > 5%.

### ULTRA-07 — Public API hygiene
- Every exported identifier in `internal/hal/`, `internal/controller/`, `internal/watchdog/`, `internal/calibrate/`, `internal/config/` should be used by at least one other package.
- Flag any exported type/func/method with zero external references.

### ULTRA-08 — Binary size drift
- `CGO_ENABLED=0 go build -o /tmp/ventd ./cmd/ventd/`
- `ls -la /tmp/ventd` — compare to last ultrareview baseline.
- Flag if total drift since last ultrareview > 500 KB or per-PR-avg > 50 KB.

### ULTRA-09 — CHANGELOG hygiene
- Every merged PR since last ultrareview has a `## Unreleased` entry.
- No duplicate entries for the same PR.
- Entry format consistent (all use past-tense action verbs, all include PR#).
- `## Unreleased` section hasn't grown beyond ~150 lines — if it has, probably time to tag a release.

### ULTRA-10 — Dependency tree audit
- `go list -m all` — full dep tree.
- `go mod tidy -v` — clean?
- `govulncheck ./...` — report everything, not just critical.
- `go.sum` consistent with `go.mod`.

### ULTRA-11 — Config schema integrity
- Every field in `config.Config` and its nested types has a `yaml` tag matching the field name.
- Every field has a validator rule in `validate()` or a documented reason why not (defaults, optional).
- Every `docs/config.md` YAML example field maps to a real struct field.
- Can a v0.1.x config still parse? (run `config.Parse` over committed v0.1 example if present)

### ULTRA-12 — Docs drift
- Every route registered in `internal/web/server.go` routes table is listed in `docs/api.md` (when that doc exists — Phase-10 addition).
- Every metric registered in Prometheus handler is in `docs/metrics.md` (when T-METRICS-01 lands).
- Every config field referenced in README.md snippets still exists.

## Report format

Single markdown file at `.cowork/reviews/ultrareview-<N>.md`:

```markdown
# Ultrareview <N>

- Triggered: <pr-count OR phase-boundary>
- Last ultrareview SHA: <sha>
- Current SHA: <sha>
- PRs audited: <list>
- Total lines changed since last: <n>

## Verdict summary

| Check | Verdict | Finding count |
|---|---|---|
| ULTRA-01 | PASS/WARN/FAIL | 0-N |
| ... | ... | ... |

## Findings

### ULTRA-XX finding 1
<description>
Severity: blocker / warning / advisory
Recommended follow-up: <new issue #N / inline fix / defer>
```

## What happens after the report

Cowork reads `.cowork/reviews/ultrareview-<N>.md`. For each finding:

- **Blocker** — halts new dispatches. A fix task is queued IMMEDIATELY as the highest priority. Dispatches resume only after the blocker is merged.
- **Warning** — filed as a GitHub issue with label `ultrareview-<N>`. Dispatches continue.
- **Advisory** — noted in the report, addressed opportunistically or folded into the next masterplan phase.

Then Cowork updates `.cowork/ULTRAREVIEW.md` to record the new `last_ultrareview_sha` and reset `pr_count_since_last` to 0.

## Cost

Ultrareview is expensive: Opus 4.7, reads most of the tree, runs multiple tools. Budget: 1 CC session of up to 2 hours. At 10 PRs / review pace, that's ~5-10% of total session time — cheap insurance against architectural drift.

## Never

- Skip an ultrareview to ship faster. The drift it catches gets exponentially more expensive to fix later.
- Auto-merge anything produced by ultrareview itself — the report is human-facing.
- Treat a WARN verdict as a FAIL — warnings are informational; dispatches continue unless a blocker fires.
- Run ultrareview at the same time as other dispatches — it needs a clean tree. Halt first, then dispatch.
