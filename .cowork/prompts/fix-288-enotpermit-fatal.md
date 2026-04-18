# fix-288-enotpermit-fatal

You are Claude Code. Restore fatal-on-permission semantics for manual-mode acquisition in the HAL hwmon backend per issue #288. Safety-critical path.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/fix-288-enotpermit-fatal origin/main
test ! -f .cowork/prompts/fix-288-enotpermit-fatal.md && echo "OK: working tree is main" || {
    echo "ERROR: cowork/state files present. Abort."
    exit 1
}
```

If the sanity check fails, stop and report.

## Context (#288 summary)

PR #247 refactored manual-mode acquisition from up-front-fatal (systemd restart loop on EACCES/EPERM) to lazy per-tick (WARN-and-continue). A sysfs permission misconfiguration — wrong apparmor profile, wrong group, SELinux label — now emits a per-tick warning instead of tripping `Restart=on-failure`. Fans stay at the BIOS curve, operator may never notice. This is a regression of the safety-visibility posture pre-#247.

## Fix (Cassidy's option 1)

Introduce a typed sentinel `hal.ErrNotPermitted`. `hwmon.Backend` wraps `EACCES`/`EPERM` from pwm_enable writes with it. Controller's `tick()` checks `errors.Is(err, hal.ErrNotPermitted)` and signals `Run()` to return fatally — systemd's `Restart=on-failure` re-engages, watchdog.Restore fires on the way down.

## Required changes

### 1. `internal/hal/backend.go` — add sentinel

Add near existing errors (import `errors` if not already imported):

```go
// ErrNotPermitted signals a permission failure during manual-mode
// acquisition (EACCES/EPERM on pwm_enable write). Callers should treat
// this as fatal — retries will not cure a misconfiguration.
var ErrNotPermitted = errors.New("hal: manual-mode acquisition not permitted")
```

### 2. `internal/hal/hwmon/backend.go` — wrap permission errors

Read `ensureManualMode` (or equivalent function that writes `pwm_enable=1`). At the error return, wrap permission errors:

```go
if err := writePwmEnable(path, 1); err != nil {
    if errors.Is(err, os.ErrPermission) || errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
        return fmt.Errorf("%w: %s", hal.ErrNotPermitted, err)
    }
    return err
}
```

Exact integration depends on the function's current shape — read it first, preserve surrounding logic, only the wrap pattern is prescriptive. Use whatever os/unix error check is idiomatic in this codebase; verify by searching for existing `os.ErrPermission` usages.

### 3. `internal/controller/controller.go` — fatal propagation

In the `tick()` Write-error branch, add a priority check BEFORE the existing retry logic:

```go
if writeErr := c.backend.Write(ch, pwm); writeErr != nil {
    if errors.Is(writeErr, hal.ErrNotPermitted) {
        c.logger.Error("controller: manual-mode acquisition denied by OS; daemon exiting for systemd restart",
            "channel", ch.ID, "err", writeErr)
        // Signal Run() to return fatally. Use whatever mechanism the controller
        // already has (fatalErr field, cancel context, errCh, etc.); do NOT call
        // os.Exit or log.Fatal from tick.
        c.signalFatal(writeErr)
        return
    }
    // ... existing retry logic (unchanged) ...
}
```

Verify the fatal-signal mechanism by reading `Run()` first. If the controller lacks one, add a bounded `fatalErr chan error` (size 1), write to it from tick via non-blocking send, read from the main Run loop alongside the ticker.

Apply the SAME check at the manual-mode Write call site in `tick()` — permission errors there are equally fatal.

### 4. Regression test

`internal/controller/controller_test.go` (or `controller_safety_test.go` if that file exists):

```go
// regresses #288
func TestController_ErrNotPermittedFatal(t *testing.T) {
    // Fake hal.Backend whose Write always returns hal.ErrNotPermitted.
    // Assert Run() returns a non-nil error after the first failing tick.
    // Assert errors.Is(returned, hal.ErrNotPermitted).
}
```

Use existing test fixtures (`testfixture/fakehwmon` or similar) as a starting pattern; the key assertion is that permission-denied does NOT cause the controller to loop-and-retry but rather to propagate fatally.

## Allowlist

- `internal/hal/backend.go`
- `internal/hal/hwmon/backend.go`
- `internal/controller/controller.go`
- `internal/controller/controller_test.go` (or the safety test file if distinct)
- `CHANGELOG.md`

NO other files. Do NOT touch `.claude/rules/hwmon-safety.md` — that file's rule-binding format is pending issue #313. If a rule-binding comment is appropriate, leave a `// TODO(issue #313): bind this invariant to hwmon-safety.md once rule format lands` note above the test.

## Verification

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/controller/... ./internal/hal/...
gofmt -l internal/hal/ internal/controller/
go vet ./internal/controller/... ./internal/hal/...
```

All four clean.

## PR

Open READY (not draft). Title: `fix(hal): ErrNotPermitted sentinel restores fatal-on-permission for manual-mode (closes #288)`

PR body must include:
- Fixes #288
- BRANCH_CLEANLINESS: `git log --oneline origin/main..HEAD` + `git diff --stat origin/main..HEAD | tail -1`
- CHANGELOG entry under BOTH `### Fixed` AND `### Security` (this is safety-posture restoration):
  - `### Fixed`: `controller: permission errors during manual-mode acquisition are now fatal, restoring pre-#247 systemd restart-loop visibility (closes #288)`
  - `### Security`: `hal: EACCES/EPERM on pwm_enable writes now propagate as hal.ErrNotPermitted, ensuring misconfigured-apparmor/SELinux scenarios surface to operators (closes #288)`

## Constraints

- Atlas merges. Do NOT merge.
- Safety-critical path. Preserve ALL existing retry semantics for non-permission errors.
- Do NOT modify watchdog.Restore behaviour. The fatal return from Run() will let the existing shutdown path fire Restore normally.
- Single commit.
- If `Run()` fatal-signal mechanism requires adding a new field/channel, keep the change minimal — do not refactor the Run loop beyond what's needed for the signal.

## Reporting

- STATUS: done | blocked
- PR URL
- `go test -race -count=1 ./internal/controller/... ./internal/hal/...` tail
- Lines changed per file
- CONCERNS if any fields of Controller struct were added (mention so Cassidy can audit)
