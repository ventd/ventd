# Controlled install pipeline rules — v0.5.9 PR-D

These invariants govern v0.5.9's controlled install pipeline that
replaced the legacy `make install` (which atomically did
cp + depmod + modprobe and made deferred signing impossible on
Secure Boot enforcing systems). The pipeline lives in
`internal/hwmon/install_steps.go`; orphan-state cleanup lives in
`internal/hwmon/cleanup.go`. The patch spec is at
`/root/.claude/plans/here-is-a-draft-curious-sutton.md`.

Each rule binds 1:1 to a subtest in
`internal/hwmon/cleanup_test.go` (cleanup contracts) plus the
existing `ootpreflight_test.go` legacy-compat suite.

## RULE-INSTALL-PIPELINE-CLEANUP-01: Cleanup removes /tmp/ventd-driver-* build dirs.

`CleanupOrphanInstall` MUST glob `$TMPDIR/ventd-driver-*` and
remove every match. Unrelated tempdirs in `$TMPDIR` MUST NOT be
touched. The report's `BuildDirsRemoved` slice records the paths
removed so the wizard surface can show the operator exactly what
was cleared before retry.

Bound: internal/hwmon/cleanup_test.go:TestRULE_INSTALL_PIPELINE_CleanupRemovesBuildDirs

## RULE-INSTALL-PIPELINE-CLEANUP-02: Cleanup is idempotent on a clean system.

`CleanupOrphanInstall` on a clean system (no orphan tmp dirs,
fake module name not loaded, no DKMS state) MUST return a
non-nil `*CleanupReport` with empty slices and no error.
Idempotence matters because the wizard's `OnFailCleanup` may
fire multiple times during a fail-retry-fail sequence.

Bound: internal/hwmon/cleanup_test.go:TestRULE_INSTALL_PIPELINE_CleanupIdempotent

## RULE-INSTALL-PIPELINE-CLEANUP-03: Blacklist drop-in writer is idempotent and append-safe.

`writeBlacklistDropIn(path, module)` MUST:
- Create the file with `blacklist <module>\n` when missing.
- No-op when the file already contains the same blacklist line.
- Append to the file (preserving existing entries) when adding
  a different module — no rewrite of unrelated lines.

The drop-in path comes from `DistroInfo.BlacklistDropInPath()`;
the writer doesn't validate the path beyond existence of the
parent dir.

Bound: internal/hwmon/cleanup_test.go:TestRULE_INSTALL_PIPELINE_BlacklistDropInIdempotent

## RULE-INSTALL-PIPELINE-CLEANUP-04: stripModuleFromLoadConf is no-op when /etc/modules-load.d/ventd.conf is absent.

`stripModuleFromLoadConf(module)` MUST return `(false, nil)`
when the canonical path doesn't exist. A missing file is the
clean-system case — there's no entry to strip, no error to
report. The (true, nil) and (false, error) branches require a
writable /etc which production tests skip when not root.

Bound: internal/hwmon/cleanup_test.go:TestRULE_INSTALL_PIPELINE_StripModuleFromLoadConf
