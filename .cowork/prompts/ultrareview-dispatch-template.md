# Ultrareview dispatch template

**Not a dispatch alias.** This is the reusable template Atlas copies when Cassidy files a `role:atlas` ultrareview-dispatch issue per protocol tracked in #331.

## When to use

Cassidy publishes `.cowork/reviews/ultrareview-N.md` with some check-rows marked `DEFERRED` (tooling-dependent checks Cassidy's claude.ai environment cannot run: deadcode, dupl, coverage, ref-counting, binary size, dep tree). Cassidy files `role:atlas` issue titled `ultrareview-N: dispatch tooling-dependent checks` citing this template.

Atlas workflow:
1. Copy this file to `.cowork/prompts/ultrareview-N-tooling.md`.
2. Replace every `{N}` with the ultrareview number.
3. Replace `{BASE_SHA}` with the exact `main` SHA Cassidy ran the static audit against (Cassidy names it in the ultrareview-N.md header).
4. Push to `cowork/state`.
5. `spawn_cc("ultrareview-N-tooling")`.

The CC session runs the six tooling checks, appends results to `ultrareview-N.md` under a new section, commits to `cowork/state`, reports back. Cassidy picks up the appended tooling results next session and closes the ultrareview.

## Template body (copy into the dispatch file, fill placeholders)

---

# ultrareview-{N}-tooling

You are Claude Code. Run the 6 tooling-dependent ULTRA checks for ultrareview-{N} and append results to the existing report.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main cowork/state
git checkout -B cc/ultrareview-{N}-tooling origin/cowork/state
# Verify we have the ultrareview report
test -f .cowork/reviews/ultrareview-{N}.md && echo "OK: report present" || {
    echo "ERROR: .cowork/reviews/ultrareview-{N}.md missing. Abort."
    exit 1
}
```

## Target SHA

All tooling checks run against `{BASE_SHA}` on origin/main (the SHA Cassidy audited statically).

```bash
git -C /tmp/ventd-ultrareview-{N} clone -q https://github.com/ventd/ventd /tmp/ventd-ultrareview-{N} 2>/dev/null || true
cd /tmp/ventd-ultrareview-{N}
git fetch origin
git checkout {BASE_SHA}
```

(If the clone already exists from a prior ultrareview, `git fetch` + `git checkout` is sufficient.)

## Checks to run

For each check: run the command, capture output, summarise findings as PASS/WARN/FAIL + enumerated issues (severity: blocker/warning/advisory).

### ULTRA-04: dead code

```bash
GO111MODULE=on go install golang.org/x/tools/cmd/deadcode@latest 2>/dev/null || true
deadcode ./... > /tmp/ultrareview-{N}-deadcode.txt 2>&1 || true
wc -l /tmp/ultrareview-{N}-deadcode.txt
```

Report: number of dead functions, top 10 by path.

### ULTRA-05: duplication

```bash
GO111MODULE=on go install github.com/mibk/dupl@latest 2>/dev/null || true
dupl -t 50 ./internal/... > /tmp/ultrareview-{N}-dupl.txt 2>&1 || true
grep -c "^found" /tmp/ultrareview-{N}-dupl.txt || true
```

Report: count of duplicate blocks ≥50 tokens, top 5 largest.

### ULTRA-06: coverage

```bash
go test -coverprofile=/tmp/ultrareview-{N}-cover.out ./... 2>&1 | tail -5
go tool cover -func=/tmp/ultrareview-{N}-cover.out | tail -20
```

Report: total coverage %, per-package for packages <70%.

### ULTRA-07: exported-identifier external-ref count

For each of `internal/hal`, `internal/controller`, `internal/watchdog`, `internal/calibrate`, `internal/config`, `internal/hwmon`:

```bash
# For each exported identifier in the package, grep for external references
# Report exports with 0 or 1 external refs (candidates for unexporting or deletion)
for pkg in internal/hal internal/controller internal/watchdog internal/calibrate internal/config internal/hwmon; do
    echo "=== $pkg ==="
    # Enumerate exports via `go doc $pkg` then grep for callers
    # Exact implementation is at CC's discretion; keep the output format consistent
done
```

Report per package: exports with ≤1 external ref.

### ULTRA-08: binary size

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tmp/ultrareview-{N}-ventd ./cmd/ventd/
ls -la /tmp/ultrareview-{N}-ventd
```

Report: current stripped size. Compare against the last ultrareview's recorded size (find in `ultrareview-{N-1}.md` if present).

### ULTRA-10: dependency tree

```bash
go list -m all | wc -l
go mod tidy -v 2>&1 | head -20
govulncheck ./... 2>&1 | tail -30
```

Report: dep count, any tidy deltas (indicates drift), vuln count by severity.

## Append to report

On `cowork/state` branch, append to `.cowork/reviews/ultrareview-{N}.md`:

```markdown
## Tooling-check results (appended by CC dispatch)

Base SHA: `{BASE_SHA}`
Dispatch timestamp: `<ISO-8601 UTC>`

### ULTRA-04 dead code
**VERDICT:** PASS | WARN | FAIL
**EVIDENCE:** <trimmed output, top findings>
**FINDINGS:**
- <list with severity>

### ULTRA-05 duplication
<same shape>

### ULTRA-06 coverage
<same shape>

### ULTRA-07 exported-ref count
<same shape>

### ULTRA-08 binary size
<same shape>

### ULTRA-10 dependency tree
<same shape>

---

Next step: Cassidy picks up these results next session and consolidates the final ultrareview-{N} verdict.
```

Do NOT modify the existing Cassidy-authored sections. Append only.

## Commit

```bash
cd /home/cc-runner/ventd
git add .cowork/reviews/ultrareview-{N}.md
git commit -m "ultrareview-{N} tooling checks (role:atlas dispatch)"
git push origin cc/ultrareview-{N}-tooling
```

Open a PR against `cowork/state`. Atlas merges into cowork/state (no CI on that branch).

## Reporting

- STATUS: done | blocked
- PR URL
- Summary: 6 check VERDICTs in a one-line table
- Total CC wall-clock

## Constraints

- Do NOT modify any code on `main`.
- Do NOT touch `.cowork/reviews/ultrareview-N.md`'s existing sections.
- If any check's tool (`deadcode`, `dupl`, `govulncheck`) can't install: record the blocker, continue with the others, flag in reporting.
- Append-only, read-only against main.
