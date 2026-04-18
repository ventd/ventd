# fix-272-manual-retry

You are Claude Code. Apply retry+RestoreOne to the manual-mode PWM write path in controller.tick() per issue #272.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/fix-272-manual-retry origin/main
test ! -f .cowork/prompts/fix-272-manual-retry.md && echo "OK: working tree is main" || {
    echo "ERROR: cowork/state files present. Abort."
    exit 1
}
```

If the sanity check fails, stop and report.

## Context (#272 summary)

PR #263 (P1-HOT-02) added retry+RestoreOne semantics to the curve-path Write in `controller.tick()`. The parallel manual-mode Write path (`if manualPWM != nil` branch in the same function) still uses the original log-and-return pattern — no retry, no RestoreOne on double failure. On a transient I/O error to a manually-overridden fan, the fan is left at the last successful PWM with no fallback to firmware auto.

CC flagged this gap in #263's PR body; this is the follow-up.

## Fix (Cassidy's recommendation)

Extract the retry+RestoreOne pattern into a helper method on `Controller`, apply to both the curve-path and manual-path Write sites.

## Required changes

### `internal/controller/controller.go`

Add helper near `tick()`:

```go
// writeWithRetry performs a backend.Write with one 50ms retry; on double
// failure it invokes watchdog.RestoreOne to hand the fan back to firmware
// auto. Returns true iff the write eventually succeeded; false iff
// RestoreOne fired. kind is a short label ("curve" | "manual") for logs.
func (c *Controller) writeWithRetry(ch hal.Channel, pwm uint8, pwmPath, kind string) bool {
    if writeErr := c.backend.Write(ch, pwm); writeErr != nil {
        c.logger.Warn("controller: PWM write failed, retrying",
            "channel", ch.ID, "kind", kind, "err", writeErr)
        time.Sleep(50 * time.Millisecond)
        if retryErr := c.backend.Write(ch, pwm); retryErr != nil {
            c.logger.Error("controller: PWM write failed after retry, triggering restore",
                "channel", ch.ID, "kind", kind, "err", retryErr)
            c.wd.RestoreOne(pwmPath)
            return false
        }
    }
    return true
}
```

(Exact signature may differ if `pwmPath` is derivable from `ch` — use whatever fields already exist in the current retry block. Match the existing curve-path implementation verbatim where possible.)

Replace the existing curve-path retry block with:

```go
if !c.writeWithRetry(ch, pwm, c.pwmPath, "curve") {
    return
}
```

Replace the manual-path block:

```go
if manualPWM != nil {
    if !c.writeWithRetry(ch, *manualPWM, c.pwmPath, "manual") {
        return
    }
    // ... existing post-write logic ...
}
```

IMPORTANT: After #288's ErrNotPermitted fatal-check lands, both call sites will need the ErrNotPermitted check placed BEFORE the writeWithRetry call — permission errors shouldn't retry. If #288 has merged by the time you read this, add that check at both sites. If #288 is still a draft, leave a `// TODO(#288): ErrNotPermitted priority check here` comment above each writeWithRetry call.

Preserve all existing pre-write / post-write logic at both sites. The helper ONLY replaces the write+retry+RestoreOne dance.

### Regression test

Add to `internal/controller/controller_test.go`:

```go
// regresses #272
func TestController_ManualWriteRetryAndRestore(t *testing.T) {
    // Fake hal.Backend whose Write returns a transient I/O error twice,
    // then succeeds on the third call. Assert: manual-mode path triggers
    // RestoreOne after two failures (same semantics as curve path).
}
```

Follow the pattern of any existing `TestController_*` test for the curve path (#263 should have added one). Mirror the assertion shape — retry invoked once, RestoreOne called on double failure.

## Allowlist

- `internal/controller/controller.go`
- `internal/controller/controller_test.go`
- `CHANGELOG.md`

NO other files. Do NOT touch `.claude/rules/hwmon-safety.md` (pending #313).

## Verification

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/controller/...
gofmt -l internal/controller/
go vet ./internal/controller/...
```

All four clean.

## PR

**Open as DRAFT.** Safety-critical path (internal/controller/). Atlas's (B) gate protocol: Cassidy audits within 24h, Atlas promotes + merges at T+24h if no blockers filed.

Title: `fix(controller): apply retry+RestoreOne to manual-mode write path via writeWithRetry helper (closes #272)`

PR body: Fixes #272, BRANCH_CLEANLINESS block, **Risk class: safety-critical**, CHANGELOG entry under `### Fixed`:

> `controller: manual-mode PWM writes now use the same retry+RestoreOne pattern as curve writes, via a new writeWithRetry helper that both sites share (closes #272)`

## Constraints

- Atlas merges. Do NOT merge. Do NOT promote to ready-for-review.
- Do NOT change retry timing (50ms) or retry count (1) — preserve #263's tuning.
- Do NOT refactor `tick()` beyond extracting the helper; the rest of the function stays identical.
- Single commit.
- If `c.pwmPath` is a single field that doesn't vary per channel, use it; if the existing retry block uses `ch.PWMPath()` or similar, match that call shape.

## Reporting

- STATUS: done | blocked
- PR URL (draft)
- `go test -race -count=1 ./internal/controller/...` tail
- Lines changed
- CONCERNS if the existing curve-path retry implementation diverged from the helper pattern in any way (e.g., different retry-count, different log shape) — report so Cassidy can audit for drift
