# State store rules

These invariants govern `internal/state/`, ventd's on-disk state
layer (KV store at `state.yaml`, blob store under `models/`, log
store under `logs/`, plus the schema-version sentinel and the PID
lock). The state directory is `/var/lib/ventd/` in production
(mode 0755, owned by `ventd:ventd`). Atomic writes, fsync-before-
rename, and a tempfile + rename pattern protect against torn
writes on power loss.

Each rule below binds 1:1 to a subtest. If a rule text is edited,
update the binding subtest in the same PR; if a new rule lands,
it must ship with a matching subtest or `tools/rulelint` blocks
the merge.

## RULE-STATE-01: KV store writes MUST use tempfile + rename + fsync semantics. Direct overwrite is forbidden.

`KVDB.persist()` writes the serialised state to a randomly-
suffixed `.tmp.*` file, calls `fsync` on it, then `os.Rename` to
atomically replace the canonical `state.yaml`. A direct
`os.WriteFile` or `os.Create` on `state.yaml` is never used. The
rename is POSIX-atomic on the same filesystem, ensuring that at
any point in time either the old file or the new file is
visible — never a partial write. `BlobDB.Write` and
`atomicWrite` use the same pattern for all persistent files in
the state directory.

Bound: internal/state/state_test.go:TestRULE_STATE_01_AtomicWrite

## RULE-STATE-02: Blob store reads MUST verify magic, length, and SHA256. Mismatch MUST result in found=false returned to consumer; consumer reinitialises.

`BlobDB.Read(name)` verifies: (1) the 4-byte magic header equals
`"VBLB"`; (2) the declared length (`uint64` at offset 8) matches
the number of payload bytes available; (3) the trailing SHA256
over the payload equals the computed `sha256.Sum256(payload)`.
On any mismatch — magic wrong, length overrun, or checksum
failure — `Read` returns `(nil, 0, false, nil)`. The consumer
interprets `found=false` as "state absent, re-initialise." This
prevents silently propagating a partially-written or
disk-damaged blob into the thermal model or RLS state.

Bound: internal/state/state_test.go:TestRULE_STATE_02_BlobSHA256Verification

## RULE-STATE-03: Log store appends MUST use `O_APPEND | O_DSYNC`. Buffered writes are forbidden for log primitive.

`logHandle.openFileLocked()` MUST open the log file with flags
`os.O_WRONLY | os.O_CREATE | os.O_APPEND | syscall.O_DSYNC`.
`O_APPEND` makes each `write(2)` syscall seek to the current
end-of-file atomically, preventing interleaved records from
concurrent writers. `O_DSYNC` ensures that data is durable on
the storage medium before the syscall returns — a crash after a
successful `Write` call will not lose that record. Buffered
writes (e.g. `bufio.Writer`) introduce a window where records
are in kernel or userspace buffers but not yet durable; this is
forbidden for the log store. Static analysis of `log.go` must
confirm both flags are present.

Bound: internal/state/state_test.go:TestRULE_STATE_03_LogOAppendODsync

## RULE-STATE-04: Log store iteration MUST tolerate torn records (length-prefix-overrun) and CRC-mismatched records (skip and continue).

`readRecords` processes a stream of length-prefix + payload +
CRC32 records:

- If the length prefix indicates a payload larger than
  `logMaxRecordSize` (64 MiB), the record is treated as torn and
  iteration stops for that file (returning nil).
- If `io.ReadFull` for the payload or CRC bytes returns
  `io.ErrUnexpectedEOF`, the record is torn (truncated at a
  crash boundary); iteration stops for that file.
- If the CRC32-IEEE of `length||payload` does not match the
  stored CRC, the record is corrupt; it is **skipped** and
  iteration continues with the next record (the stream position
  is still valid because all bytes were consumed).

This ensures that a crash mid-append loses at most one record
and does not prevent access to the records that follow it.

Bound: internal/state/state_test.go:TestRULE_STATE_04_LogTornRecordSkip

## RULE-STATE-05: Schema version on read MUST be checked. Y > X (downgrade) MUST refuse start with diagnostic. Y < X (upgrade) MUST run registered migration or treat as missing.

`CheckVersion(dir string)` reads the integer in `dir/version`:

- **Missing file**: write `currentVersion`, return nil (first
  run).
- **on-disk == currentVersion**: return nil.
- **on-disk > currentVersion**: return an error wrapping
  `ErrDowngrade` with a human-readable message that names both
  versions and instructs the operator to reinstall a newer
  binary or run `ventd state reset`. The daemon must not start
  when it encounters a state directory written by a future
  version.
- **on-disk < currentVersion**: apply each registered
  `MigrateFn` from version `v` to `currentVersion` sequentially.
  If no migration is registered for a step, the state is treated
  as missing (consumers re-initialise). Update the sentinel to
  `currentVersion` and return nil.

Bound: internal/state/state_test.go:TestRULE_STATE_05_SchemaVersionCheck
Bound: internal/state/state_test.go:downgrade_refused
Bound: internal/state/state_test.go:upgrade_no_migration_treats_as_missing
Bound: internal/state/state_test.go:same_version_ok
Bound: internal/state/state_test.go:first_run_creates_sentinel

## RULE-STATE-06: Multiple ventd processes against the same state directory MUST be detected via PID file; second process MUST exit with diagnostic.

`AcquirePID(dir)` writes the current process PID to
`dir/ventd.pid` and returns a `release` func that removes it on
daemon shutdown. If `ventd.pid` already exists and contains a
PID that responds to `kill(pid, 0)` with no error (i.e. the
process is alive), `AcquirePID` returns
`*ErrAlreadyRunning{PID: pid}` immediately without writing. The
caller in `cmd/ventd/main.go` treats this as a fatal startup
error, exiting with a log message that names the conflicting
PID. A stale PID file (process no longer alive) is removed and
replaced. This prevents two daemon instances from racing over
the same `state.yaml`, which would produce lost writes under the
tempfile+rename pattern (the last rename wins, discarding
intermediate state).

Bound: internal/state/state_test.go:TestRULE_STATE_06_PIDFileMultiProcess

## RULE-STATE-07: KV `WithTransaction` MUST serialise to a single atomic write at commit. Partial commits across failure are forbidden.

`KVDB.WithTransaction(fn)` deep-copies the current in-memory
state into a `KVTx` snapshot, calls `fn(tx)`, and — only if
`fn` returns nil — replaces `db.data` with `tx.data` and calls
`db.persist()` once. If `fn` returns a non-nil error, `db.data`
is left unchanged and `persist()` is never called. The
combination of a single `persist()` call and the `atomicWrite`
helper (tempfile+rename) ensures that either the
pre-transaction state or the fully-committed post-transaction
state is on disk — never a partial state containing
some-but-not-all of the transaction's mutations. Tests verify
both the success path (all keys visible after commit) and the
rollback path (no keys visible after failure).

Bound: internal/state/state_test.go:TestRULE_STATE_07_TransactionAtomicCommit

## RULE-STATE-08: Log rotation MUST NOT lose in-flight records. Atomic rename + new file creation, no append-after-rename window.

`logHandle.rotateLocked()` executes the following sequence
while holding `h.mu`:

1. Close the current file handle (no further writes possible).
2. Shift existing rotated files: `.keepCount` deleted, `.N-1` →
   `.N`, …, `.1` → `.2`.
3. `os.Rename(logPath, logPath+".1")` — atomic POSIX rename.
4. `h.openFileLocked()` — create and open a new `logPath` for
   future appends.
5. (Optional, background) gzip-compress `.1` if size > 10 MiB.

Because `h.mu` is held for the entire sequence, no `Append`
call can write to the old file after step 1, and no `Append`
can write to the new file before step 4. This eliminates the
append-after-rename window where a record written to the old
file path after the rename would appear in `.1` without the
caller knowing. `Iterate` collects both the current file and
all rotated files, so all records written before and after a
rotation are visible during iteration.

Bound: internal/state/state_test.go:TestRULE_STATE_08_LogRotationNoRecordLoss

## RULE-STATE-09: All state files MUST be created with mode `0640 ventd ventd`; directories `0755 ventd ventd`. Mode mismatches on read MUST be repaired (not refused) to handle umask quirks during install.

`atomicWrite` creates files with mode `0640` (`fileMode`).
`initDirs` creates directories with mode `0755` (`dirMode`). If
`state.yaml` already exists on disk with a different mode (e.g.
`0600` from a restrictive umask), `openKV` calls `repairMode()`
which detects the mismatch via `os.Stat` and applies
`os.Chmod(path, 0640)` before loading the file. The daemon logs
a warning but continues normally — refusing to start because of
a mode mismatch would break systems where the installer or
sysadmin created the file with a different umask. The repair
guarantees that the diag bundle process (which reads via group
membership) can access state files after the first daemon
restart.

Bound: internal/state/state_test.go:TestRULE_STATE_09_FileModeRepair

## RULE-STATE-10: The state directory `/var/lib/ventd/` MUST exist after first daemon start; absence triggers initialisation, not failure.

`state.Open(dir, logger)` calls `initDirs(dir)` which uses
`os.MkdirAll` to create:

- `dir/` (mode 0755)
- `dir/models/` (mode 0755)
- `dir/logs/` (mode 0755)

If `dir` does not exist, `initDirs` creates the full hierarchy.
If it already exists, `os.MkdirAll` is a no-op. A missing state
directory is therefore not an error — it is the normal
first-boot condition. All three stores (KV, Blob, Log) are
initialised with empty state on a fresh directory. This allows
the daemon to start cleanly after `rm -rf /var/lib/ventd/`
without requiring manual directory creation or a special
`--reset` flag.

Bound: internal/state/state_test.go:TestRULE_STATE_10_DirectoryBootstrap

## RULE-STATE-11: `SchemaVersionLoaded()` reports true only after a clean `openKV` load with an acceptable schema; nil receivers report false.

`state.Open` runs `CheckVersion(dir)` (RULE-STATE-05: downgrade
refused, upgrade migrated) BEFORE `openKV`, and `openKV` sets the
`schemaOK` flag only after `load()` returns without error (a clean
parse, or a valid empty first boot). So reaching a constructed
`KVDB` with `schemaOK == true` means the persisted schema is
trustworthy. `(*KVDB).SchemaVersionLoaded()` and the
`(*State).SchemaVersionLoaded()` passthrough are nil-safe and
return false on a nil receiver.

This is the "state schema loaded" term of the v0.5.9
`w_pred_system` global gate (spec-v0_5_9 §2.5): the confidence
controller keeps predictive control off until persisted state is
loaded and trustworthy.

Bound: internal/state/state_test.go:TestKV_SchemaVersionLoaded

## RULE-STATE-12: KV writes refuse before mutating in-memory state when the state directory has less than iox.MinFreeBytesForState bytes free.

`KVDB.Set`, `KVDB.Delete`, and `KVDB.WithTransaction` all call
`ensureFreeSpaceFn(filepath.Dir(db.path))` BEFORE acquiring
`db.mu` and BEFORE mutating `db.data`. Production points the
seam at `iox.EnsureFreeSpace(_, iox.MinFreeBytesForState)`
(1 MiB threshold).

The pre-flight gate is structurally distinct from the
persist-time write: it can be tightened, loosened, or stubbed
in tests via the `ensureFreeSpaceFn` package-level seam. The
seam exists so unit tests can exercise the refusal path without
requiring an actually-low-space filesystem; production code
never reassigns it.

The pre-flight position (before mutex, before mutation) is
load-bearing. Prior to this rule, `Set` mutated
`db.data[ns][key] = value` first (`kv.go:100`) and called
`persist()` second (`kv.go:101`); a `persist` failure on ENOSPC
returned the error to the caller but left the in-memory map
advanced. On daemon restart, `load()` read the OLD on-disk
value while the runtime had been quietly running on the NEW
value — the silent in-memory/on-disk divergence the
senior-review's C7 finding identified for
`wizard.initial_outcome`, calibration state, polarity records,
and the smart-mode shard root.

The refusal preserves the existing transactional contracts:

- `Set` and `Delete` return the wrapped
  `iox.ErrInsufficientFreeSpace` error; subsequent `Get`
  returns the original (un-mutated) value.
- `WithTransaction` refuses BEFORE invoking the caller's `fn`
  closure, so an operator that does expensive work inside
  `fn` doesn't burn the cycles only to discover the commit
  can't land. RULE-STATE-07's "fn-returns-error leaves the
  world untouched" extends to "low-disk returns
  iox.ErrInsufficientFreeSpace before fn ever runs".
- The on-disk `state.yaml` is unchanged on the refusal path
  (no `atomicWrite` is attempted).

The threshold is shared across all three call paths so a single
operator-tunable knob (future v0.6.0 `state.yaml` directive)
can adjust the entire KV's refusal behaviour without per-call
overrides. TOCTOU between the pre-flight statfs and the
eventual `atomicWrite` is acceptable: the gate catches the
common case (disk has been near-full for a while) rather than
racing against pathological "another process filled the disk in
50µs" scenarios — that case is caught by `atomicWrite`'s own
ENOSPC error return on the actual write, which
Set/Delete/WithTransaction propagate verbatim to the caller.

Bound: internal/state/state_test.go:RULE-STATE-12_set_refuses_before_in_memory_mutation
Bound: internal/state/state_test.go:RULE-STATE-12_delete_refuses_before_in_memory_mutation
Bound: internal/state/state_test.go:RULE-STATE-12_with_transaction_refuses_before_calling_fn
Bound: internal/state/state_test.go:RULE-STATE-12_seam_restored_after_test_lets_subsequent_writes_pass

## RULE-STATE-13: The state directory is resolved through `EffectiveDir()`, which honours the `VENTD_STATE_DIR` override; every daemon entry point uses it so the pidfile and all stores follow one override together.

`state.DefaultDir` (`/var/lib/ventd`) is the production default,
but the daemon never hardcodes it at the call site: `AcquirePID`,
`Open`, the last-fatal sentinel (`cmd/ventd`), and the diag
observation export all resolve the directory through
`state.EffectiveDir()`. `EffectiveDir()` returns the trimmed
`VENTD_STATE_DIR` value when set, else `DefaultDir`; a single
override therefore redirects the pidfile **and** the KV, blob,
and log stores consistently, so the RULE-STATE-06 collision
check operates on the chosen directory rather than splitting
state across two locations.

The override is a **dev/test seam**, not a production knob — its
purpose is to let a second daemon run against a synthetic hwmon
tree (`VENTD_HWMON_ROOT`, `tools/hwmonsim`) without contending on
the production daemon's pidfile and stores. It mirrors
`hwmon.RootOverrideEnv`. When the override is active the daemon
logs a loud one-time WARN (`DirIsOverridden()`), so a stray
setting in production can't silently fragment state; in a real
deployment the systemd unit's `ReadWritePaths` / AppArmor profile
also confine writes to `DefaultDir`, blocking an override there.

Bound: internal/state/state_test.go:RULE-STATE-13_effective_dir_resolves_override

## RULE-STATE-MIGRATION-V1-V2-NOOP: A registered no-op v1→v2 migrator preserves caller state across the version bump and exercises the migration mechanism end-to-end.

`migrations[[2]int{1, 2}]` MUST be a registered, callable no-op
that returns nil without touching any file in the state
directory. The version-2 schema is identical to version-1 on
disk; the bump exists to reserve the v2 slot for the v0.6.0
broker-namespace migration (and any other v0.6 breaking shape
change) without triggering RULE-STATE-05's "treat as missing"
path.

A registered no-op is structurally distinct from a missing
migrator. RULE-STATE-05 specifies that when no migration is
registered for a step, the upgrade loop breaks out and the
caller's state is effectively wiped on next access:

> If no migration is registered for a step, the state is
> treated as missing (consumers re-initialise).

That semantic is correct for additive-only changes that the
caller can re-derive, but is wrong for the v0.6 transition
where existing calibration / polarity / smart-mode shards must
survive. Registering an explicit no-op:

1. Keeps the upgrade loop walking forward (consumers' state is
   preserved).
2. Exercises the migration mechanism end-to-end so the first
   real migration that lands (v2→v3, broker-namespace shape)
   drops in against a tested loop rather than against a dead
   code path.
3. Pins `currentVersion = 2` so the v0.6 release line is
   unambiguous about which schema slot it occupies.

The migrator itself is `noopV1ToV2(dir string) error { return nil }`
in `internal/state/version.go`. It is registered via the
`migrations` map literal (not `RegisterMigration`) because the
version is internal to the state package — external packages
that introduce schema changes still use `RegisterMigration` at
init time.

The `currentVersion` constant is bumped to `2` in the same
change. A regression that reverts `currentVersion` to `1`
makes the v1→v2 migrator dead code and undoes the
broker-namespace reservation; the bound subtest catches that
regression explicitly.

Bound: internal/state/state_test.go:v1_to_v2_migrator_is_registered
Bound: internal/state/state_test.go:upgrade_v1_to_currentVersion_runs_migrator_and_bumps_sentinel
Bound: internal/state/state_test.go:noop_migrator_does_not_mutate_sibling_files_in_state_dir
Bound: internal/state/state_test.go:currentVersion_is_at_least_2
