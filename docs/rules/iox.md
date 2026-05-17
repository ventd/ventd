# iox atomic-write helper rules — v0.5.11 R28 Stage 1.5 PR-3

These invariants govern `internal/iox`, the canonical home for the
ventd-wide atomic-file-write primitive. Prior to this PR, eleven
packages each had their own near-duplicate `tempfile + write +
fsync + rename` helper; nine of them omitted the post-rename
parent-directory fsync, leaving the directory entry vulnerable to
power-loss-after-rename loss on consumer SSDs that batch metadata
writes.

`iox.WriteFile` consolidates the pattern and adds the missing
dir-fsync. Every persistent write in ventd MUST go through it so
the durability contract is uniform.

Each rule binds 1:1 to a subtest in
`internal/iox/atomicwrite_test.go`.

## RULE-IOX-01: WriteFile is atomic, idempotent on overwrite, leak-free, and creates missing parent directories.

`iox.WriteFile(path, data, mode)` MUST satisfy ALL of:

1. **Round-trip integrity.** A subsequent `os.ReadFile(path)` returns
   `data` byte-equal, and `os.Stat(path).Mode().Perm()` matches the
   requested `mode`.

2. **No tempfile leak.** After a successful return, the parent
   directory contains no `.tmp.<hex>` siblings of the destination.
   The cleanup must run on every error path before the rename and
   must not run after a successful rename.

3. **Atomic overwrite.** A second call to the same path replaces the
   contents in a single rename — no half-written intermediate state
   is observable to a concurrent reader.

4. **Parent directory creation.** Missing parents are created with
   `DefaultDirMode` (0755). Operators rm-ing the state dir mid-run
   shouldn't break the next persist.

5. **Post-rename dir-fsync** (the load-bearing invariant). After
   `os.Rename` succeeds, the function MUST `os.Open(dir).Sync()` so
   the rename's directory entry is durable on power loss. Failure of
   the dir-sync is swallowed because the rename already committed
   correctness; the fsync is durability insurance.

The bound subtests cover (1) round-trip, (2) leak, (3) overwrite,
(4) parent-creation. The dir-fsync is structural (every path through
the helper hits it) — re-asserting it in a unit test would require
a mock filesystem, deferred until a future PR if a regression
emerges.

Bound: internal/iox/atomicwrite_test.go:TestWriteFile_RoundTrip
Bound: internal/iox/atomicwrite_test.go:TestWriteFile_NoTempLeak
Bound: internal/iox/atomicwrite_test.go:TestWriteFile_OverwritesExisting
Bound: internal/iox/atomicwrite_test.go:TestWriteFile_CreatesParentDir

## RULE-IOX-02: EnsureFreeSpace returns wrapped ErrInsufficientFreeSpace when the filesystem holding `path` has fewer than `minBytes` available.

`iox.EnsureFreeSpace(path, minBytes)` is the pre-flight gate every
state-class write path consults BEFORE mutating in-memory state. The
canonical use case is `KVDB.Set` / `KVDB.Delete` / `KVDB.WithTransaction`
(per RULE-STATE-12), but any future caller that stages an in-memory
mutation followed by a disk-write should adopt the same gate.

Behavioural contract:

- **Healthy filesystem (`avail >= minBytes`)**: returns `nil`.
- **Low-space filesystem (`avail < minBytes`)**: returns an error
  wrapping `ErrInsufficientFreeSpace` whose message names the path,
  the measured `avail` bytes, and the required `minBytes`. Operators
  reading the journal can correlate the refusal to a specific
  filesystem without taking a separate measurement.
- **Path doesn't exist** (statfs returns ENOENT, etc.): returns the
  underlying statfs error WITHOUT wrapping `ErrInsufficientFreeSpace`.
  Callers can distinguish "we couldn't measure" from "we measured and
  it's too low" via `errors.Is(err, ErrInsufficientFreeSpace)`. The
  doctor card path uses this distinction: only the wrapped case
  warrants a "free up disk space" remediation.
- **`minBytes == 0`**: short-circuits to `nil` before any syscall, so
  callers wanting to disable the gate (tests, future operator-tunable
  override) pass 0 rather than a sentinel.

`MinFreeBytesForState` (1 MiB) is the canonical default for state-class
writes. Tight enough that healthy systems essentially never see refusals,
large enough to leave headroom for the tempfile + final + dir-fsync
sequence even if the marshalled payload grows by an order of magnitude
relative to what the daemon writes today.

Implementation uses `syscall.Statfs` directly. `Bavail × Bsize` gives
the free bytes available to the daemon's user; on every Linux filesystem
ventd targets, `Bsize` equals the fragment size in bytes.

Bound: internal/iox/freespace_test.go:RULE-IOX-02_happy_path_returns_nil
Bound: internal/iox/freespace_test.go:RULE-IOX-02_zero_minimum_short_circuits
Bound: internal/iox/freespace_test.go:RULE-IOX-02_missing_path_surfaces_underlying_error
Bound: internal/iox/freespace_test.go:RULE-IOX-02_huge_minimum_refuses_with_actionable_error
Bound: internal/iox/freespace_test.go:RULE-IOX-02_works_on_file_path_not_just_dir
