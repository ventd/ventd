You are Claude Code running an ULTRAREVIEW on the ventd repository.

This is NOT a regular task. You are auditing the ENTIRE tree for architectural drift, dead code, duplication, and coherence issues that per-PR review missed.

## Mode

- Model: Opus 4.7 (heaviest reasoning needed).
- Time budget: up to 2 hours wall-clock. Don't rush.
- Output: ONE markdown report at `.cowork/reviews/ultrareview-<N>.md` on the `cowork/state` branch.
- Do NOT modify any production code, test code, or rule files. This is read-only.
- You are NOT opening a PR. You are pushing one file to cowork/state.

## Determining <N>

1. List existing ultrareviews: `ls .cowork/reviews/ultrareview-*.md 2>/dev/null`.
2. `<N>` = (highest existing number) + 1, or `1` if none exist.

## Setup

```
cd /home/cc-runner/ventd
git fetch origin main cowork/state
git checkout main
git pull origin main
```

## The 12 checks

Run each check in the order below. For each, record: **VERDICT** (PASS / WARN / FAIL), **EVIDENCE** (the specific files/lines/grep output), and **FINDINGS** (enumerated issues with severity: blocker/warning/advisory).

### ULTRA-01 — HAL contract coherence

Read `internal/hal/backend.go` (the interface) and every backend implementation (`internal/hal/hwmon/*.go`, `internal/hal/nvml/*.go`, and any others that have landed).

Cross-check:
- Is every method `Enumerate / Read / Write / Restore / Close / Name` present on every backend?
- Does `Restore` have the same idempotency semantics across backends?
- Does `Read` ever mutate state? (It shouldn't.)
- Does `Write` always clamp inputs? Look for missing clamps.
- Is `Caps` stable across `Enumerate` calls? (Backends should return the same Caps for the same channel between calls.)

Optional: read `.claude/rules/hal-contract.md` (if it exists after T-HAL-01 lands) and cross-ref every rule to implementation.

### ULTRA-02 — Safety posture coherence

- `grep -rn "^go func\|go \w*(" internal/ cmd/` — list every goroutine spawn in production code (exclude tests). For each, verify there's a ctx.Done() or stop-channel exit condition somewhere in the same function.
- `grep -rn "defer.*Restore\|wd\.Restore" internal/ cmd/` — confirm every exit path in `internal/controller/` and `cmd/ventd/main.go` invokes watchdog restore.
- `grep -rn "log\.Fatal\|os\.Exit\|panic(" internal/ cmd/` — flag any outside `cmd/*/main.go` as a blocker.
- `grep -rn "defer.*Close" internal/` — confirm every `os.Open`/`os.Create`/similar has a paired Close.

### ULTRA-03 — Rule file integrity

- List `.claude/rules/*.md` files.
- Each should start with `# <Scope> Invariants` and contain `## RULE-<ID>:` sections with `Bound:` lines.
- Count rules per file; flag files with >20 rules (probably needs splitting).
- Cross-ref every `Bound:` line to its claimed test file — does the subtest exist? (This duplicates what `tools/rulelint` already does; if rulelint is green, just note that and move on.)
- Read every rule in every file and look for contradictions (rare but high-impact).

### ULTRA-04 — Dead code

```
go install golang.org/x/tools/cmd/deadcode@latest
$HOME/go/bin/deadcode ./...
```

Report every reported dead function. For each, note whether it's:
- Genuinely dead (blocker to remove).
- Referenced only by tests (warning — should probably be test-only).
- Exported and intended for third-party use (advisory).

Also grep for exported identifiers in `internal/` that have zero external references:

```
for pkg in internal/hal internal/controller internal/watchdog internal/calibrate internal/config; do
  echo "=== $pkg ==="
  grep -rhoE "^func (\\(.*\\))? ?[A-Z][a-zA-Z0-9]*" $pkg | sort -u > /tmp/exported.txt
  # check each exported name against the rest of the tree
  while read fn; do
    name=$(echo "$fn" | grep -oE "[A-Z][a-zA-Z0-9]*$")
    count=$(grep -rn "\\b$name\\b" --include="*.go" . | grep -v "$pkg/" | wc -l)
    echo "$count\t$name"
  done < /tmp/exported.txt | sort -n | head -20
done
```

List any exports with 0-1 external references (the 1-ref case is often a test; still worth flagging).

### ULTRA-05 — Duplication audit

```
go install github.com/mibk/dupl@latest
$HOME/go/bin/dupl -t 50 ./internal/...
```

Report every duplicated block ≥ 50 tokens. Pay special attention to cross-backend duplication in `internal/hal/*/` — that's the primary target for extraction to `internal/hal/common/`.

### ULTRA-06 — Test coverage map

```
go test -coverprofile=/tmp/cover.out ./... 2>&1 | tail -20
go tool cover -func=/tmp/cover.out | tail -50
```

Report per-package coverage. Flag anything below 80% as warning; flag anything below 50% as blocker (watchdog, controller, calibrate, hal packages specifically).

Compare to the previous ultrareview's coverage table if one exists (`.cowork/reviews/ultrareview-<N-1>.md`). Flag any package that regressed >5%.

### ULTRA-07 — Public API hygiene

For each of `internal/hal`, `internal/controller`, `internal/watchdog`, `internal/calibrate`, `internal/config`, `internal/hwmon`:
- List exported identifiers.
- Count external references per identifier.
- Flag any with zero external references (that's dead API).

### ULTRA-08 — Binary size drift

```
CGO_ENABLED=0 go build -o /tmp/ventd ./cmd/ventd/
ls -la /tmp/ventd
```

Record the size. If a previous ultrareview report exists, compare. Flag drift:
- <100 KB delta: advisory.
- 100-500 KB: warning.
- >500 KB: blocker (investigate which PRs contributed).

### ULTRA-09 — CHANGELOG hygiene

- Read `CHANGELOG.md`.
- Count entries under `## [Unreleased]`.
- Cross-ref every merged PR since last ultrareview against CHANGELOG entries. Flag missing entries as warnings.
- Flag duplicate entries (same PR# in two places) as warnings.
- If `## Unreleased` has >150 lines, suggest a release tag.

### ULTRA-10 — Dependency tree

```
go list -m all | wc -l
go mod tidy -v 2>&1 | head
govulncheck ./... 2>&1 | tail -30
```

Report:
- Total direct + transitive deps.
- Any `tidy` output (should be empty).
- All vulnerabilities reported by govulncheck (not just critical; report all).

### ULTRA-11 — Config schema integrity

- Read `internal/config/config.go` (or wherever the main Config struct lives).
- For each field, note whether it has a yaml tag, whether it's referenced in `validate()`, and whether it appears in `config.example.yaml`.
- Flag any field without a yaml tag, without a validator, or not in the example.
- Read `docs/config.md` if present; cross-ref every documented field against the struct.
- If a v0.1 example config is available (look in test fixtures or git history), confirm it still parses via `go test -run TestLoad_v01Compat ./internal/config/` or similar; note the result.

### ULTRA-12 — Docs drift

- `grep -rn "mux\\.Handle\\|mux\\.HandleFunc\\|r\\.HandleFunc\\|s\\.router" internal/web/` — list every registered route.
- Cross-ref against `docs/api.md` (if it exists).
- Check README.md code/config snippets against current code — do the field names still match?

## Producing the report

Create `.cowork/reviews/ultrareview-<N>.md` with this structure:

```markdown
# Ultrareview <N>

- **Triggered:** <reason — "10 PRs since last" / "phase-boundary" / "manual">
- **Date:** <ISO-8601>
- **Last ultrareview SHA:** <commit SHA of last ultrareview OR "none (first)">
- **Current main SHA:** <output of `git rev-parse main`>
- **PRs audited since last ultrareview:** <list, e.g. #251-#260>
- **Lines changed since last:** <output of `git diff --stat <last-sha>..main | tail -1`>

## Verdict summary

| Check | Verdict | Blockers | Warnings | Advisories |
|---|---|---|---|---|
| ULTRA-01 HAL contract | PASS/WARN/FAIL | N | N | N |
| ULTRA-02 Safety posture | ... | | | |
| ULTRA-03 Rule files | ... | | | |
| ... | | | | |
| ULTRA-12 Docs drift | ... | | | |

## Findings

### ULTRA-01 finding 1 (severity: blocker/warning/advisory)

<Description>

**Evidence:**
```
<file path + line numbers + relevant code snippet>
```

**Recommended follow-up:** <specific action — new issue #N, inline fix in next CC dispatch, defer to Phase X, etc.>

### ULTRA-02 finding 1 ...

## Raw data

Put heavy output (deadcode full list, coverage per-package, govulncheck full report) in this section at the bottom so the findings above stay scannable.

## Recommended next actions

1. <blocker> — must be fixed before next dispatch.
2. <warning> — file as GitHub issue with label `ultrareview-<N>`.
3. <warning> — ...
4. <advisory> — note only.

## Summary

<One paragraph: overall health, biggest concern, whether the tree is ready for continued development.>
```

## Pushing the report

```
cd /home/cc-runner/ventd
git checkout cowork/state
git pull origin cowork/state
mkdir -p .cowork/reviews
# Write the report to .cowork/reviews/ultrareview-<N>.md
git add .cowork/reviews/ultrareview-<N>.md
git commit -m "ultrareview-<N>: <one-sentence summary of top finding>"
git push origin cowork/state
```

## Reporting back to Cowork

STATUS: done | partial | blocked
REPORT_PATH: .cowork/reviews/ultrareview-<N>.md
BLOCKER_COUNT: <n>
WARNING_COUNT: <n>
ADVISORY_COUNT: <n>
TOP_FINDING: <one sentence describing the single highest-severity finding>
TIME_SPENT: <approximately X hours>

## Out of scope

- Do NOT fix anything you find. This is audit-only.
- Do NOT open any PRs.
- Do NOT modify any file except the ultrareview report itself.
- Do NOT run tests on hardware — all checks are static/unit-test level.
- Do NOT recurse into third-party dep source — `go list -m all` is enough.
