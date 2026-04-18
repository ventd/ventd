# fix-293-sensor-fan-collision

You are Claude Code. Implement the fix described in issue #293.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/fix-293-sensor-fan-collision origin/main
```

Abort if `.cowork/prompts/` is in your working tree; you're on the wrong worktree. Run `git status` first.

## Task

`internal/config/config.go` `validate()` must reject configs where a sensor.name equals a fan.name. Currently both maps populate independently without cross-checking. Add the intersection check.

### Required change in internal/config/config.go

After both `sensors` and `fans` maps are populated in `validate()`, add:

```go
for name := range fans {
    if _, clash := sensors[name]; clash {
        return fmt.Errorf("config: %q is used as both a sensor name and a fan name; names must be unique across sensors and fans so history keyspace stays unambiguous", name)
    }
}
```

Place it immediately after the loops that populate those maps. No other changes to validate().

### Required test

Add a regression test in `internal/config/config_test.go` (or the closest existing validate-test file). Name it something like `TestValidate_RejectsSensorFanNameCollision`. The test:

1. Builds a valid minimal config with one sensor named `"cpu"` and one fan named `"cpu"`.
2. Calls `validate()` directly (or via `config.Parse` if that's the standard path).
3. Asserts the error is non-nil and contains the substring `"sensor name and a fan name"` or similar marker.
4. Add `// regresses #293` on the line above the test function.

Also add a positive-case companion test `TestValidate_AllowsDistinctSensorFanNames` that passes validation with distinct names (so the negative test isn't confirming a blanket reject). This companion does not need the `// regresses` annotation.

## Allowlist

- `internal/config/config.go`
- `internal/config/config_test.go` (and possibly a sibling `*_test.go` file if validate tests live elsewhere — search for existing `TestValidate` functions first)
- `CHANGELOG.md`

No other files.

## Verification

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/config/...
gofmt -l internal/config/
go vet ./internal/config/...
```

All four must be clean.

## PR

Open the PR ready (not draft). Title: `fix(config): reject sensor/fan name collisions (closes #293)`.

PR body must include:
- Fixes `#293`
- BRANCH_CLEANLINESS block showing `git log --oneline origin/main..HEAD`
- TEST_MATRIX showing the two test names and what they pin
- CHANGELOG entry under `## [Unreleased] / ### Fixed`

## Constraints

- Do NOT merge the PR. Atlas merges via MCP.
- Do NOT touch `internal/web/history.go` — the issue mentions it as context but the fix is config-layer.
- Do NOT namespace the HistoryStore keys — rejected in the issue body as too invasive.
- Single commit. Squash anything else if the worktree had stray changes.

## Reporting

- STATUS: done | blocked
- PR URL.
- Output of `go test -race -count=1 ./internal/config/...` tail.
- Lines changed.
