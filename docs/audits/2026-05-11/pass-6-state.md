# Pass 6: internal/state/ deep read

**Files audited:** state.go (109), kv.go (242), log.go (425), blob.go (107), version.go (99), pidfile.go (62)
**LOC:** 1044 non-test (1888 incl. blanks)
**Time on task:** ~30 min
**Baseline commit:** b46c1a5 (post pass-1..5)

The state package is — by design — the smallest amount of code carrying the most contract weight in the daemon. Six files, each with a sharply-defined scope: atomic write, KV+tx, blob+checksum, append-log+rotation, version sentinel, PID lock. The audit confirms the **load-bearing contracts are all in place** (tempfile+rename+dir-fsync, `O_APPEND|O_DSYNC`, SHA256 on read, CRC skip-and-continue, free-space pre-flight before mutation, registered v1→v2 no-op). The findings below are real but secondary — they cluster on durability gaps in the **secondary write paths** (log file creation, version-sentinel write, PID file write) that bypass the consolidated `iox.WriteFile` / `atomicWrite` helper and therefore miss its dir-fsync.

The single most consequential finding is **C1: the secondary write paths bypass `iox.WriteFile` and miss the dir-fsync**, leaving exactly the failure class RULE-IOX-01 was created to close.

## Critical findings

### C1 — `version.go:writeVersion` uses `os.WriteFile`, bypasses dir-fsync (RULE-IOX-01 violated)
**File:** `/root/ventd-work/internal/state/version.go:97-99`
```go
func writeVersion(path string, v int) error {
    return os.WriteFile(path, []byte(strconv.Itoa(v)+"\n"), fileMode)
}
```
The version sentinel is written via plain `os.WriteFile` — no tempfile, no fsync, no dir-fsync. RULE-IOX-01 explicitly mandates that "every persistent write in ventd MUST go through `iox.WriteFile`". This is also the smoking-gun stale-comment case: the `CheckVersion` comment at line 51 references the contract, but the implementation skips it.

**Failure mode under power loss:** A daemon writing `currentVersion = 2` for the first time (or after a successful v1→v2 migration loop) may have the sentinel file's directory entry batched. A power loss between `os.WriteFile` returning and the next sync can leave the directory in its prior state. On reboot the binary re-runs the migration loop — for the registered v1→v2 no-op that's fine, but the contract for future real migrations (the v2→v3 broker-namespace migration RULE-STATE-MIGRATION-V1-V2-NOOP reserves) is that a successful migration moves the sentinel forward atomically. A torn write makes that contract conditional on hardware that doesn't batch metadata.

**Suggested action:** Replace `os.WriteFile` with `iox.WriteFile`. The change is one line.

### C2 — `pidfile.go:AcquirePID` uses `os.WriteFile`, bypasses both atomic-rename and dir-fsync
**File:** `/root/ventd-work/internal/state/pidfile.go:45-48`
```go
content := strconv.Itoa(os.Getpid()) + "\n"
if err := os.WriteFile(path, []byte(content), fileMode); err != nil {
    return nil, fmt.Errorf("state: write pid file: %w", err)
}
```
The PID file write is **not** atomic (a concurrent reader could see a zero-length file) and skips the dir-fsync. RULE-STATE-06's contract on `kill(pid, 0)`-based detection only works if the file's content survives crashes — a torn PID file (zero bytes) returns parse failure → "stale, replace" → a second daemon instance silently wins the race against a still-alive first instance.

**Failure mode:** Power loss during PID-file write → second daemon at next boot reads zero bytes, treats as stale, takes the lock. Both daemons now race over `state.yaml`. The RULE-STATE-06 binding test exercises the alive/stale dispatch but not the torn-write path.

**Suggested action:** Use `iox.WriteFile` for the PID write. The atomic-rename closes the torn-write gap. (Production-grade: also lock via `flock(2)` LOCK_EX|LOCK_NB on a `.lock` file, but that's beyond the audit scope.)

### C3 — `log.go:logHandle.openFileLocked` and `handle()` open append-log files with no dir-fsync of `logs/` after creation
**File:** `/root/ventd-work/internal/state/log.go:98-101, 257-261`
```go
f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_DSYNC, fileMode)
```
`O_APPEND | O_DSYNC` is correct per RULE-STATE-03 (data durability), but the **directory entry** for a freshly-`O_CREATE`'d log file is not fsync'd. Subsequent `Append` calls fsync data via `O_DSYNC`, but the file's *presence in the directory* is metadata that consumer SSDs may batch. Power loss after Append→success but before any directory-metadata flush can leave the log file invisible after reboot — every record that was durably written to its data blocks is then unrecoverable because there's no directory entry pointing at the inode.

The same gap exists in `rotateLocked`'s call to `openFileLocked` (line 237) — the new active log file gets no parent fsync.

**Failure mode:** Daemon starts on fresh install, writes calibration / observation records, then power-loss; on reboot the `logs/` directory contains no entry for `envelope.log` despite the inode existing on disk.

**Suggested action:** After `os.OpenFile` for a newly-created log file (detect via `info.Size() == 0` before any write, or unconditionally on first-open), open `db.dir` and `f.Sync()` its parent. RULE-STATE-08's rotation path also benefits from a post-`os.Rename` parent dir fsync (currently neither the rotate-old-to-`.N+1` nor the rename-to-`.1` syncs the parent).

## High findings

### H1 — `log.go:rotateLocked` rename chain has no fsync of the directory between shifts; rotation under power-loss can leave duplicate/missing files
**File:** `/root/ventd-work/internal/state/log.go:218-227`
The shift loop renames `.N-1 → .N`, `.N-2 → .N-1`, … `.1 → .2`, all under `h.mu` (good) but with no dir-fsync between the shifts or after the final rename. Power loss mid-shift can leave the rotated set in an inconsistent state — e.g. both `.1` and `.2` pointing to the same content (if `.1 → .2` succeeded but the directory entry batch was lost) or `.1` missing entirely.

**Suggested action:** Single parent-dir fsync after all shifts complete, before opening the new file. One syscall covers the entire batch.

### H2 — `log.go:logHandle.appendRecord` builds the full record buffer via `make + 3× copy`; consider `(*os.File).Writev` for atomicity guarantee
**File:** `/root/ventd-work/internal/state/log.go:149-156`
The 3-buffer record (length || payload || CRC) is assembled into a single `[]byte` then written via `h.f.Write`. This is technically correct — `O_APPEND` makes the single `write(2)` atomic for the seek-to-EOF, and the kernel's atomic-up-to-PIPE_BUF guarantee (technically only for pipes, but de facto on regular files for typical record sizes) means a partial write is rare. However, the code does NOT check for short writes: `n, err := h.f.Write(rec)` discards `n` if `err == nil`. A partial-write success (n < len(rec)) under unusual filesystem conditions would silently corrupt the next record.

**Suggested action:** Either explicitly check `n == len(rec)` and treat short-write as error, or document that the contract relies on the kernel's atomic-write guarantee for regular files under O_APPEND below the page boundary. The 64 MiB `logMaxRecordSize` cap means a 16 MiB record (well above page) is admissible without short-write protection.

### H3 — `kv.go:WithTransaction` rollback path leaves caller-side mutations to `tx.data` invisible to the caller
**File:** `/root/ventd-work/internal/state/kv.go:202-215`
```go
db.mu.Lock()
defer db.mu.Unlock()
snap := kvDeepCopy(db.data)
tx := &KVTx{data: snap}
if err := fn(tx); err != nil {
    return err
}
db.data = tx.data
return db.persist()
```
Correctness is fine — the deep copy isolates `db.data` from mid-fn writes. But the implementation holds **the full `db.mu` write lock for the entire duration of `fn`** including expensive caller work. RULE-STATE-12's amendment said "an operator that does expensive work inside fn doesn't burn the cycles only to discover the commit can't land" — but the pre-flight is FREE-SPACE only; a slow `fn` that does work after the pre-flight still blocks every `Get`/`List` on the same KV for its entire duration. The RWMutex doesn't help because the lock is `Lock()`, not `RLock()`.

**Failure mode:** A wizard `fn` that takes 100ms of synchronous YAML transform inside `WithTransaction` stalls every smart-mode shard read for 100ms.

**Suggested action:** Take the snapshot under a brief lock, release, run `fn` against the snapshot lock-free, then re-acquire to commit. Standard MVCC pattern. Out of scope for a pure audit pass but worth flagging.

### H4 — `kv.go:load` silently treats YAML unmarshal failure as "empty state"
**File:** `/root/ventd-work/internal/state/kv.go:78-82`
```go
var top map[string]any
if err := yaml.Unmarshal(raw, &top); err != nil {
    db.logger.Warn("state: kv: corrupt state.yaml, treating as empty", "path", db.path, "err", err)
    return nil
}
```
A corrupt `state.yaml` (e.g. half-written from a pre-RULE-STATE-01 release, or filesystem-damaged) is silently downgraded to "empty state, first boot semantics". This is **correct** for many fields (calibration / probe state will get re-derived) but **silently destructive** for `wizard.initial_outcome` and `polarity` records that drive the wizard fork decision — operators get a fresh wizard run with no warning that the prior state was destroyed.

**Failure mode:** Operator restarts daemon after a power-loss event, sees the setup wizard again, doesn't know prior state was discarded. The polarity records (inverted-fan detection from RULE-POLARITY-08) are silently re-defaulted to "unknown" → all inverted fans run wrong-direction until the next polarity probe.

**Suggested action:** When YAML parse fails, rename the corrupt file to `state.yaml.corrupt-<ts>` before treating as empty, so the operator can manually recover or send to support. The RULE-STATE-02 blob "found=false → re-initialise" pattern intentionally accepts data loss; this KV path should be more conservative because KV state isn't checksummed.

## Medium findings

### M1 — `state.go:atomicWrite` predates the iox consolidation; package-local duplicate of `iox.WriteFile`
**File:** `/root/ventd-work/internal/state/state.go:74-109`
The `atomicWrite` function in this package is a duplicate of `iox.WriteFile` — same tempfile + rename + dir-fsync pattern, slightly different error wrapping. RULE-IOX-01's preamble said "eleven packages each had their own near-duplicate; nine omitted the dir-fsync. `iox.WriteFile` consolidates the pattern". `internal/state` has one of the two correct copies but has not been migrated to the canonical helper. Maintenance burden: a future fix to `iox.WriteFile` (e.g. better tempfile cleanup semantics, support for `O_TMPFILE` on Linux 3.11+) won't reach `state.go`.

**Suggested action:** Replace `atomicWrite` with `iox.WriteFile`. Both blob.go and kv.go indirect through `atomicWrite`; the migration is mechanical.

### M2 — `blob.go:Read` does NOT verify the declared `length` against the file's actual size
**File:** `/root/ventd-work/internal/state/blob.go:58-77`
RULE-STATE-02 mandates verification of "(2) the declared length matches the number of payload bytes available". The current implementation reads `length` from the header, then does `io.ReadFull` on a buffer of that size — meaning "available bytes" is consumed verbatim from the file. A blob whose header declares `length = 4096` but whose file is only `header(16) + payload(2048) + sha256(32) = 2096` bytes would fail at `io.ReadFull` for the payload with `ErrUnexpectedEOF`, which is correctly converted to `found=false`. But a blob whose file has **extra trailing bytes after the SHA256** (e.g. concatenation corruption) silently passes — only the first `length` bytes are checksummed; trailing garbage is invisible.

**Failure mode:** Niche but real on filesystems with unusual truncation semantics or after a partial overwrite. The SHA256 check catches payload tampering but not "extra bytes after the SHA".

**Suggested action:** After reading the SHA256, do `f.Read(make([]byte, 1))` and expect `io.EOF`; treat non-EOF as corrupt. Defence-in-depth, low cost.

### M3 — `log.go:Iterate` calls `iteratePath` which silently swallows non-ENOENT errors
**File:** `/root/ventd-work/internal/state/log.go:334-342`
```go
f, err := os.Open(path)
if os.IsNotExist(err) {
    return nil
}
if err != nil {
    return nil  // <-- ALL errors silently swallowed
}
```
An EACCES (permission denied — could happen if log file mode was tampered), EIO (disk error), or EMFILE (too many open files) on `os.Open` is treated identically to ENOENT: skip the file, return nil. The caller has no signal that records may have been lost.

**Suggested action:** Log permission/IO errors via `db.logger.Warn` before returning nil; only ENOENT is truly silent.

### M4 — `log.go:compressFile` runs in background goroutine without context propagation
**File:** `/root/ventd-work/internal/state/log.go:244-249`
```go
go func(path string) {
    if gzErr := compressFile(path); gzErr != nil {
        h.logger.Warn("state: log: compress rotated file failed", "path", path, "err", gzErr)
    }
}(rotated)
```
The compression goroutine is fire-and-forget — no `context.Context`, no `sync.WaitGroup` on daemon shutdown. On `state.Close()`, an in-flight `gzip` of a 100 MiB log file continues writing to disk after the daemon has returned from `main`. systemd's `KillMode=process` reaps the goroutine eventually, but the partial `.gz` file (if interrupted mid-write) is then a corrupt artifact that `iteratePath` (line 344-351) will silently skip — losing records that were in the un-finalised gzip but had been successfully `os.Remove`'d from the plain rotated file (line 424).

**Failure mode:** Daemon shutdown during background compression → records that were durably in the plain `.1` file are now in a corrupt `.1.gz` that gzip.NewReader cannot decompress → records lost.

**Suggested action:** Either (a) make compression synchronous under `h.mu` (simple, slight latency), (b) keep the plain `.1` file alongside `.1.gz` until verified valid, then remove plain, or (c) add a shutdown waitgroup. Option (b) is the cheapest correctness fix.

### M5 — `kv.go:repairMode` log message can fire on every daemon start if a process outside ventd repeatedly resets the mode
**File:** `/root/ventd-work/internal/state/kv.go:62-66`
The repair is `os.Chmod(path, 0640)` — correct. The log level is `Info`, fine. But there's no guard against a misbehaving operator setting `state.yaml` to `0600` between every daemon start; RULE-STATE-09 says "logs a warning but continues normally", which the code does, but the audit notes the lack of any escalation if the mode keeps drifting back. Not actionable; flagged for completeness.

### M6 — `state.go:Open` does NOT call `CheckVersion` until *after* `initDirs` succeeds, leaving a window where directories exist but version sentinel doesn't
**File:** `/root/ventd-work/internal/state/state.go:38-43`
First-boot ordering: `initDirs` succeeds, `CheckVersion` then writes the version sentinel. If the daemon crashes between these two calls (or a power loss interleaves), the state directory exists with no `version` file → next boot reads "missing version", writes `currentVersion`, treats as first boot. Correct outcome. **But** if the first boot also wrote `state.yaml` (impossible today; flagging for future-proofing), the version-missing path would treat existing state as first-boot and re-initialise.

**Suggested action:** None today; the order is currently safe because no writes happen between `initDirs` and `CheckVersion`. Document the invariant in the file header so a future contributor doesn't insert a write here.

## Low findings

### L1 — `log.go:appendRecord` increments `h.appended` and checks rotation under the same lock, fine, but the `% logRotationCheckEvery` check means rotation can be deferred by up to 99 records past `MaxSizeMB`
**File:** `/root/ventd-work/internal/state/log.go:161`
A 100 MiB cap with 1 MiB records → rotation can fire at 199 MiB on a tight loop. Performance optimisation that's deliberate per the constant name. Flagged because the rule text says "rotation when file exceeds this size" and the implementation says "approximately". Acceptable.

### L2 — `blob.go:Write` does NOT use `bytes.Buffer.WriteTo` or similar; allocates the full `content` slice up-front
**File:** `/root/ventd-work/internal/state/blob.go:92-95`
For typical blob sizes (<1 MiB smart-mode shards) this is fine. For a hypothetical 100 MiB blob the double-allocation (caller's payload + this concatenation) is wasteful. Not actionable today.

### L3 — `pidfile.go:isProcessAlive` uses `syscall.Signal(0)` which can return EPERM for processes owned by a different user
**File:** `/root/ventd-work/internal/state/pidfile.go:56-62`
A stale PID that has been reused by a process owned by another user (rare but possible in shared environments) returns "alive" (EPERM, not ESRCH), and `AcquirePID` refuses to start. The contract says "process responds to kill(pid, 0) with no error" so this is correct per spec, but the operator-facing error message ("another ventd process is already running") is misleading. Out-of-scope but worth documenting.

### L4 — `state.go:Close` only closes `Log`; does not flush KV or Blob (correctly — they have no buffer)
**File:** `/root/ventd-work/internal/state/state.go:54-56`
No issue; flagged for the test catalogue to confirm there's no future write-buffering regression.

## Verified-correct

The audit explicitly verified the following load-bearing invariants are correctly implemented:

- **RULE-STATE-01 / RULE-IOX-01 (atomic write):** `state.go:atomicWrite` (lines 74-109) implements tempfile + write + fsync + close + rename + parent-dir fsync. The dir-fsync at lines 104-107 is the load-bearing piece and IS present. `blob.go:Write` and `kv.go:persist` both indirect through this helper.
- **RULE-STATE-02 (blob verify-on-read):** `blob.go:Read` (lines 35-77) verifies magic ("VBLB"), reads declared length, reads payload, reads SHA256, compares — any mismatch returns `(nil, 0, false, nil)`. Consumer-side check that `found=false` re-initialises is consumer's responsibility (pass-2 verified for coupling/marginal Save+Load; signature/layer_a remain unwired ghosts).
- **RULE-STATE-03 (O_APPEND|O_DSYNC):** `log.go:handle()` line 98 and `log.go:openFileLocked` line 258 both pass `os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_DSYNC`. No `bufio.Writer` anywhere in the file. Static analysis: confirmed via grep.
- **RULE-STATE-04 (torn-record tolerance):** `log.go:readRecords` (lines 358-394) handles three cases per spec: length-prefix EOF → return nil (stop); payload EOF → return nil; CRC mismatch → `continue` to next record. The 64 MiB cap at line 365 bounds malformed lengths.
- **RULE-STATE-05 (version dispatch):** `version.go:CheckVersion` (lines 59-95) implements the four cases — missing → write currentVersion + nil; equal → nil; greater → ErrDowngrade-wrapped error; less → migration loop with break-on-missing-migrator semantics matching the rule text.
- **RULE-STATE-06 (PID file detection):** `pidfile.go:AcquirePID` (lines 29-51) reads existing file, parses PID, calls `isProcessAlive` (kill(pid, 0)), returns `*ErrAlreadyRunning` for live PID; removes stale and writes own. Correct dispatch. (See C2 above for the durability gap on the write side.)
- **RULE-STATE-07 (transaction atomicity):** `kv.go:WithTransaction` (lines 202-215) deep-copies, runs fn, commits via single persist() on success, drops snapshot on error. Single `persist()` call ensures `atomicWrite`'s rename is the only on-disk mutation. (H3 above is a performance concern, not a correctness regression.)
- **RULE-STATE-08 (rotation no-loss):** `log.go:rotateLocked` (lines 194-255) holds `h.mu` throughout: close → shift `.N-1` files → rename current to `.1` → reopen → optional compress. No append-after-rename window. (H1 above is a durability concern on the rename chain, not a correctness regression.)
- **RULE-STATE-09 (file mode repair):** `kv.go:repairMode` (lines 54-68) stats the file, compares perm to `fileMode (0640)`, chmods on mismatch, logs Info. Called before `load()` so the parsed file is the repaired one.
- **RULE-STATE-10 (directory bootstrap):** `state.go:initDirs` (lines 58-69) uses `os.MkdirAll` on the three required dirs with `dirMode (0755)`. Idempotent on existing dirs. Called from `Open` before any other access.
- **RULE-STATE-12 / RULE-IOX-02 (free-space pre-flight):** `kv.go` lines 112, 128, 203 — `Set`, `Delete`, `WithTransaction` all call `ensureFreeSpaceFn(filepath.Dir(db.path))` **before** `db.mu.Lock()` and **before** any mutation. The seam pattern (package-level `var ensureFreeSpaceFn`) matches the rule's description. Pre-flight position is load-bearing and IS load-bearing here.
- **RULE-STATE-MIGRATION-V1-V2-NOOP:** `version.go:13` sets `currentVersion = 2`; `version.go:35-43` registers `noopV1ToV2` in the `migrations` map literal; `noopV1ToV2(dir string) error { return nil }` matches the rule's no-op contract. Walking forward through `CheckVersion`'s upgrade loop reaches and invokes it.
- **Concurrency: KV map under WithTransaction:** the deep-copy at line 208 isolates `db.data` from `tx.data` mutations inside `fn`; concurrent `Get`/`Set`/`Delete` block on `db.mu.RLock()`/`db.mu.Lock()` until commit. No race window on `db.data` between snapshot and commit. (The `kvDeepCopy` helper at lines 232-242 is shallow on values — fine because values are scalars and YAML-marshallable composites; a caller that stores a `*[]byte` and mutates the underlying slice mid-transaction would race, but that's a caller contract violation not a state-package bug.)
- **ENOSPC behaviour:** `atomicWrite` returns the underlying error from `os.Rename` (line 100-102) — verified to be the wrapped ENOSPC from the rename. KV `persist()` propagates it. RULE-STATE-12 keeps `db.data` advanced when the pre-flight admits but the actual rename racing-fails; per the rule's amendment that's "TOCTOU acceptable: caught by atomicWrite's own ENOSPC return" — which it is.

## Files NOT audited and why

- `internal/state/state_test.go` — read for **context only**, not audited as production code per the task scope. Verified that the bound subtests named in the rule files exist (TestRULE_STATE_01..12, the v1→v2 migrator subtests, the RULE-STATE-12 sub-subtests).
- No other files in `internal/state/` exist; the audit set is complete.

---

**Top 10 most consequential findings (priority order):**

1. **C1** — `writeVersion` bypasses iox.WriteFile (dir-fsync missing on version sentinel)
2. **C2** — `AcquirePID` bypasses atomic write (torn-PID-file race)
3. **C3** — Log file creation skips parent-dir fsync (records durable, file invisible)
4. **H1** — Log rotation rename chain skips dir-fsync between shifts
5. **H4** — YAML unmarshal failure silently destroys polarity/wizard records without operator signal
6. **H3** — `WithTransaction` holds db.mu for entire fn duration; blocks all reads
7. **M1** — Package-local `atomicWrite` duplicates `iox.WriteFile`; maintenance debt
8. **M4** — Background gzip goroutine on rotation has no shutdown sync → corrupt `.1.gz` swallows records on shutdown
9. **M2** — Blob Read doesn't reject trailing bytes after SHA256
10. **H2** — `appendRecord` doesn't check for short writes
