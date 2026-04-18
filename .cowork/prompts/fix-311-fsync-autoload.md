# fix-311-fsync-autoload

You are Claude Code. Add fsync durability to `mergeModuleLoadFile` in `internal/hwmon/autoload.go` per issue #311.

## Branch setup

```bash
cd /home/cc-runner/ventd
git fetch origin main
git checkout -B claude/fix-311-fsync-autoload origin/main
# Sanity check: cowork/state files must not be present
test ! -f .cowork/prompts/fix-311-fsync-autoload.md && echo "OK: working tree is main" || {
    echo "ERROR: working tree contains cowork/state files. Abort."
    exit 1
}
```

If the sanity check fails, stop immediately and report.

## Task

`internal/hwmon/autoload.go` function `mergeModuleLoadFile` uses a write-to-temp → rename pattern that is correct for directory-entry atomicity but skips fsync on the temp file and on the parent directory. On a kernel panic or power loss between rename and the next natural disk sync, the rename metadata can land in the journal while the temp file's data blocks don't. Result: after reboot, `/etc/modules-load.d/ventd.conf` exists but contains zero bytes. `systemd-modules-load.service` loads nothing; ventd's fan access breaks until the next probe-install cycle.

## Required change in `internal/hwmon/autoload.go`

In `mergeModuleLoadFile`, after the `WriteString` call and before `tmp.Close()`, add `tmp.Sync()`. After the `os.Rename` call, add a best-effort parent-directory fsync.

Replace the existing block:

```go
if _, err := tmp.WriteString(sb.String()); err != nil {
    _ = tmp.Close()
    return fmt.Errorf("write %s: %w", tmpPath, err)
}
if err := tmp.Close(); err != nil {
    return fmt.Errorf("close %s: %w", tmpPath, err)
}
if err := os.Chmod(tmpPath, 0644); err != nil {
    return fmt.Errorf("chmod %s: %w", tmpPath, err)
}
if err := os.Rename(tmpPath, path); err != nil {
    return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
}
```

With:

```go
if _, err := tmp.WriteString(sb.String()); err != nil {
    _ = tmp.Close()
    return fmt.Errorf("write %s: %w", tmpPath, err)
}
if err := tmp.Sync(); err != nil { // regresses #311: fsync data before rename
    _ = tmp.Close()
    return fmt.Errorf("sync %s: %w", tmpPath, err)
}
if err := tmp.Close(); err != nil {
    return fmt.Errorf("close %s: %w", tmpPath, err)
}
if err := os.Chmod(tmpPath, 0644); err != nil {
    return fmt.Errorf("chmod %s: %w", tmpPath, err)
}
if err := os.Rename(tmpPath, path); err != nil {
    return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
}
// Best-effort: fsync parent directory to persist the rename metadata.
// Wrapped in open-succeeded check because some filesystems don't support
// dir fsync; failure here is not an error — the rename already happened.
if dirFile, err := os.Open(dir); err == nil { // regresses #311: parent dir fsync
    _ = dirFile.Sync()
    _ = dirFile.Close()
}
```

Note: `dir` is the directory component of `path`. Verify that `dir` is already computed in scope; if not, derive it with `filepath.Dir(path)`. Do not add a new import for `path/filepath` if it is already imported; check first.

Do NOT add a test for the fsync behavior. The issue explicitly notes that verifying fsync requires a block-layer fault injector not available in the test suite. No test addition is needed for this fix.

## Allowlist

- `internal/hwmon/autoload.go`
- `CHANGELOG.md`

No other files.

## Verification

```bash
CGO_ENABLED=0 go build ./...
go test -race -count=1 ./internal/hwmon/...
gofmt -l internal/hwmon/
go vet ./internal/hwmon/...
```

All four must be clean.

## PR

Open ready (not draft). Title: `fix(hwmon): fsync tmp file and parent dir before/after rename in mergeModuleLoadFile (closes #311)`

PR body must include:
- Fixes `#311`
- BRANCH_CLEANLINESS block: paste output of `git log --oneline origin/main..HEAD` and `git diff --stat origin/main..HEAD | tail -1`
- CHANGELOG entry under `## [Unreleased] / ### Fixed`

## Constraints

- Do NOT merge the PR. Atlas merges.
- Do NOT touch any test files — no fsync test is feasible without a fault injector.
- Do NOT address the `os.CreateTemp` 0600→0644 chmod race mentioned in the issue out-of-scope section.
- Single commit.

## Reporting

- STATUS: done | blocked
- PR URL
- `go test -race -count=1 ./internal/hwmon/...` tail
- Lines changed
