# spec-16 — Persistent runtime state foundation

**Status:** SHIPPED in v0.5.1 (PR #669, commit 4983278).
Drafted 2026-04-27 (un-deferred from `spec-16-persistent-state-DEFERRED.md`).
Originally planned as v0.5.0.1; bundled into v0.5.1 release tag.
**Supersedes:** `spec-16-persistent-state-DEFERRED.md`.
**Consumed by:** v0.5.4 passive observation logging (primary new
consumer); v0.5.6 workload signature library; v0.5.8 Layer C RLS
state; v0.5.9 confidence state per channel; v0.5.10 doctor recovery
events. Existing code (calibration cache) migrates as touched.
**References:** `spec-smart-mode.md` (design of record for smart-mode
architecture).

---

## 1. Why this ships first

The smart-mode patch sequence cannot proceed past v0.5.3 without
persistence. v0.5.4 (passive observation logging) requires append-only
log persistence by definition. v0.5.6 (workload signatures), v0.5.8
(Layer C RLS state), and v0.5.9 (confidence state) all require
durable state across daemon restarts — re-learning from cold start at
every boot defeats the smart-mode value proposition.

Spec-16 was previously deferred under the assumption that smart-mode
would be deferred. With smart-mode promoted to the v0.6.0 critical
path, spec-16 is no longer deferrable — it is the prerequisite.

Shipping spec-16 standalone (rather than bundled into v0.5.4) gives
v0.5.4 a clean primitive to build against and avoids retrofitting
storage shape decisions when later patches surface needs not
anticipated by the first consumer.

---

## 2. Scope and non-scope

### 2.1 In scope

- Three storage shapes covering all known consumers:
  - **KV store** — small structured records, hot-read.
  - **Binary blob store** — opaque byte arrays for serialised state
    (thermal model coefficients, RLS estimator state).
  - **Append-only log** — high-volume sequential writes (passive
    observation, drift events).
- File layout under `/var/lib/ventd/`.
- Atomic write semantics for KV and binary stores.
- Append semantics with crash safety for log store.
- Schema versioning for forward and backward compatibility.
- Sysusers integration with the existing `ventd` system user (per
  spec-06).
- Permission model consistent with AppArmor profile.
- Test coverage for corrupt state, missing state, schema mismatch,
  partial writes, permission denied.

### 2.2 Out of scope

- **Network synchronisation.** State is local-only.
- **User-initiated backup/restore tooling.** Future spec; sysadmin
  uses standard `tar` of `/var/lib/ventd/` for now.
- **Encryption at rest.** State files are not secrets; readable by
  `ventd` user only via standard filesystem permissions.
- **Cross-distro state migration.** Each install has its own
  `/var/lib/ventd/`.
- **Database backend (sqlite, lmdb, etc.).** All storage is plain
  files. See §6 for rationale.
- **Migration of existing calibration cache schema.** The current
  calibration cache continues to work; it migrates onto the new KV
  store opportunistically when later patches touch it. Not a v0.5.0.1
  goal.

---

## 3. File layout

```
/var/lib/ventd/
├── state.yaml          KV store — small structured records
├── state.yaml.tmp      Atomic-write temp (transient)
├── models/             Binary blob store — one file per blob
│   ├── thermal.dat     spec-05 thermal model coefficients (later patches)
│   ├── coupling.dat    Layer B coupling map
│   └── layer_c.dat     Layer C RLS estimator state
├── logs/               Append-only log store
│   ├── observations.log    Passive observation stream (v0.5.4 primary)
│   ├── observations.log.1  Rotated log (after rotation)
│   └── events.log          Drift events, envelope aborts, etc.
└── version             Schema version sentinel (single integer)
```

All paths under `/var/lib/ventd/` owned by `ventd:ventd`, mode `0755`
on directories, `0640` on files (group `ventd` for diag bundle read
access).

---

## 4. Storage shape — KV store

### 4.1 Format

Single YAML file at `/var/lib/ventd/state.yaml`. Top-level keys are
namespaces; namespace contents are arbitrary YAML-serialisable values.

```yaml
schema_version: 1

ventd:
  install_id: "uuid-v4-here"
  first_install: "2026-04-27T15:30:00Z"
  last_clean_run: "2026-04-28T09:15:32Z"

calibration:
  channel_states:
    "hwmon3:pwm1":
      last_calibration_envelope: "C"
      polarity: "normal"
      firmware_version: "ASUS X670E 1604"
      # ...

experimental:
  flags:
    amd_overdrive:
      enabled: true
      first_enabled: "2026-04-27T16:00:00Z"
      ack_revert_required: false

wizard:
  completed_at: "2026-04-27T15:45:00Z"
  initial_outcome: "control_mode"

telemetry:
  consent: "granted"
  last_submission: "2026-04-27T20:00:00Z"
```

### 4.2 Atomic write

KV writes use **tempfile + rename** semantics:

1. Read full state into memory.
2. Apply update.
3. Serialise to `/var/lib/ventd/state.yaml.tmp`.
4. `fsync` tempfile.
5. `rename` tempfile → `state.yaml` (atomic on POSIX).
6. `fsync` directory.

Renames are atomic on the same filesystem on Linux. If `/var/lib/ventd/`
is a tmpfs or weird filesystem (NFS), rename is still atomic per
filesystem rules — not ventd's problem to handle exotic mount points.

### 4.3 Read concurrency

Single in-process reader-writer lock guards the in-memory copy. ventd
is single-process; multiple processes against the same state directory
is unsupported. If detected (PID file), second process exits.

### 4.4 API shape

```go
package state

type KV interface {
    Get(namespace, key string) (any, bool, error)
    Set(namespace, key string, value any) error
    Delete(namespace, key string) error
    List(namespace string) (map[string]any, error)
    WithTransaction(fn func(tx KVTx) error) error
}
```

`WithTransaction` batches multiple reads/writes into a single
serialise/atomic-write cycle.

---

## 5. Storage shape — Binary blob store

### 5.1 Format

One file per blob under `/var/lib/ventd/models/`. Each file is opaque
bytes prefixed by a fixed 16-byte header:

```
offset  size  field
0       4     magic         "VBLB" (ventd blob)
4       2     schema_version  uint16, big-endian
6       2     reserved      zeros
8       8     length        uint64, big-endian — payload length
16      N     payload       opaque bytes (binary-serialised data)
N+16    32    sha256        sha256 of payload (verification)
```

### 5.2 Atomic write

Same tempfile + rename semantics as KV. Magic + version + length +
payload + checksum written to tempfile, fsynced, renamed atomically.

### 5.3 Verification on read

Header magic verified. If magic mismatch → file is corrupt or alien;
treated as missing. Length must match file size. SHA256 verified
against payload; on mismatch, treated as corrupt; consumer
re-initialises blob state.

### 5.4 API shape

```go
package state

type BlobStore interface {
    // Read returns (payload, schema_version, found, error).
    // found=false means file missing or corrupt-treated-as-missing.
    Read(name string) ([]byte, uint16, bool, error)

    Write(name string, schema_version uint16, payload []byte) error
    Delete(name string) error
}
```

Consumers handle their own serialisation. Spec-16 does not impose
encoding (consumers may use `gob`, `protobuf`, custom binary, etc.).

---

## 6. Storage shape — Append-only log store

### 6.1 Format

Log files under `/var/lib/ventd/logs/`. Each file is a sequence of
length-prefixed records:

```
record:
  4 bytes   length         uint32, big-endian
  N bytes   payload        opaque (consumer-defined; recommended: msgpack or json-line)
  4 bytes   crc32          IEEE checksum of length+payload
```

Records appended with `O_APPEND | O_DSYNC` to provide crash-consistent
append. On crash, last record may be torn — verified via CRC on read,
torn record discarded.

### 6.2 Rotation

Logs rotate when:

- File size exceeds threshold (default 100 MB), OR
- File age exceeds threshold (default 30 days), OR
- Caller invokes `Rotate(name)`.

Rotation: `mv observations.log observations.log.1`, create new
`observations.log`. Old rotation files compressed with gzip on
rotation if file size > 10 MB after rotation.

Retention: keep last N rotated files (default 5). Configurable per
log via `RotationPolicy`.

### 6.3 API shape

```go
package state

type LogStore interface {
    Append(name string, payload []byte) error
    Iterate(name string, since time.Time, fn func(payload []byte) error) error
    Rotate(name string) error
    SetRotationPolicy(name string, policy RotationPolicy) error
}

type RotationPolicy struct {
    MaxSizeMB    int
    MaxAgeDays   int
    KeepCount    int
    CompressOld  bool
}
```

`Iterate` is for offline analysis. The hot path (append-and-forget) is
the `Append` call; iteration runs from background goroutines or
diagnostic tools, never the control loop.

`Iterate` MUST traverse rotated files within retention transparently:
when `since` predates the active file's first record, the
implementation reads matching rotated files (including gzip-compressed
`.gz` siblings) in chronological order before reaching the active
file. Files whose mtime is older than `since` are skipped. This is
required for `Stream(since=72h)` semantics in the v0.5.4 observation
log consumer.

### 6.4 Crash safety

Per-record CRC catches torn records. Log readers must skip records
where:

- Length prefix would extend past end of file → torn; truncate and
  resume.
- CRC mismatches → corrupt record; skip and continue.

Consumers must tolerate skipped records — passive observation loses a
data point, drift event log loses an entry. No crash recovery beyond
"skip the bad record, continue." This is acceptable because:

- Append-only logs are advisory, not authoritative.
- Authoritative state lives in KV (atomic) or BlobStore (atomic +
  checksummed).
- Loss of one observation in a stream of millions is statistically
  insignificant for Layer A/B/C learning.

---

## 7. Schema versioning

### 7.1 Top-level version sentinel

`/var/lib/ventd/version` contains a single integer — the
**install schema version**. Bumped when ventd makes a breaking change
to the on-disk layout that requires migration.

### 7.2 Per-store versioning

- KV store: `schema_version` field at top of `state.yaml`.
- Blob store: `schema_version` field in 16-byte header per blob.
- Log store: no per-record version; log files are advisory and can be
  truncated/discarded on incompatible version bump.

### 7.3 Version compatibility rules

ventd at version X reading state written by version Y:

- **Y == X**: read normally.
- **Y < X**: ventd applies forward migration if registered; otherwise
  treats state as missing and re-initialises.
- **Y > X**: ventd refuses to start, surfaces "downgrade detected"
  diagnostic. User must reinstall newer ventd or wipe state.

Forward migration is a registered function per (from, to) version pair.
Most consumers will register null migrations (additive YAML fields are
backward-compatible without migration).

### 7.4 Initial version

v0.5.0.1 ships with `version: 1`. Consumers added in v0.5.4+ extend
the v1 schema additively. Schema version bumps to 2 only when an
incompatible change is necessary.

---

## 8. Sysusers and permissions

### 8.1 System user

ventd already declares the `ventd` system user via sysusers (per
spec-06 install contract). spec-16 reuses this user for state file
ownership.

### 8.2 Directory creation

ventd creates `/var/lib/ventd/` and subdirectories on first start if
absent. Mode `0755` for directories. systemd-tmpfiles entry ensures
correct ownership across reboots:

```
# /usr/lib/tmpfiles.d/ventd.conf
d /var/lib/ventd        0755 ventd ventd -
d /var/lib/ventd/models 0755 ventd ventd -
d /var/lib/ventd/logs   0755 ventd ventd -
```

### 8.3 AppArmor profile updates

The existing AppArmor profile (per spec-06) allows write access to
`/var/lib/ventd/`. This spec extends the profile entries to cover the
new `models/` and `logs/` subdirectories. RULE-APPARMOR-* entries
updated accordingly.

### 8.4 File modes

- Directories: `0755 ventd ventd`
- KV store (`state.yaml`): `0640 ventd ventd` (group readable for
  diag bundle process).
- Blob store files: `0640 ventd ventd`.
- Log files: `0640 ventd ventd`.

Diag bundle (existing P9 redactor) reads logs via group access. State
files are not user-readable without group membership.

---

## 9. Invariant bindings (RULE-STATE-* in `.claude/rules/`)

| Rule ID | Statement |
|---|---|
| `RULE-STATE-01` | KV store writes MUST use tempfile + rename + fsync semantics. Direct overwrite is forbidden. |
| `RULE-STATE-02` | Blob store reads MUST verify magic, length, and SHA256. Mismatch MUST result in `found=false` returned to consumer; consumer reinitialises. |
| `RULE-STATE-03` | Log store appends MUST use `O_APPEND \| O_DSYNC`. Buffered writes are forbidden for log primitive. |
| `RULE-STATE-04` | Log store iteration MUST tolerate torn records (length-prefix-overrun) and CRC-mismatched records (skip and continue). |
| `RULE-STATE-05` | Schema version on read MUST be checked. Y > X (downgrade) MUST refuse start with diagnostic. Y < X (upgrade) MUST run registered migration or treat as missing. |
| `RULE-STATE-06` | Multiple ventd processes against the same state directory MUST be detected via PID file; second process MUST exit with diagnostic. |
| `RULE-STATE-07` | KV `WithTransaction` MUST serialise to a single atomic write at commit. Partial commits across failure are forbidden. |
| `RULE-STATE-08` | Log rotation MUST NOT lose in-flight records. Atomic rename + new file creation, no append-after-rename window. |
| `RULE-STATE-09` | All state files MUST be created with mode `0640 ventd ventd`; directories `0755 ventd ventd`. Mode mismatches on read MUST be repaired (not refused) to handle umask quirks during install. |
| `RULE-STATE-10` | The state directory `/var/lib/ventd/` MUST exist after first daemon start; absence triggers initialisation, not failure. |

Each rule maps 1:1 to a Go subtest in `internal/state/state_test.go`
or sibling files. `tools/rulelint` enforces the binding.

---

## 10. Implementation surface

### 10.1 New package

`internal/state/` — new top-level package implementing the three
stores.

```
internal/state/
├── state.go         Top-level State struct; opens KV+Blob+Log.
├── kv.go            KV store implementation.
├── blob.go          Blob store implementation.
├── log.go           Log store implementation.
├── version.go       Schema version + migration registry.
├── pidfile.go       Multi-process detection.
└── state_test.go    RULE-STATE-* subtests.
```

### 10.2 Daemon integration

`cmd/ventd/main.go` initialises `state.State` early in startup (after
config load, before HAL init). Subsystems requiring persistence
receive a state.State reference at construction.

### 10.3 Existing calibration cache

The existing calibration cache (`internal/calibration/cache`) does
**not** migrate to the new KV store in v0.5.0.1. It continues to work
with its current shape (per-channel JSON files). Migration happens
opportunistically when v0.5.3 (Envelope C/D) reworks calibration
storage.

This is deliberate: the v0.5.0.1 patch ships infrastructure, not
consumer migrations. Forcing the calibration cache to migrate during
spec-16 introduces unrelated risk and inflates the patch.

---

## 11. Failure modes enumerated

1. **`/var/lib/ventd/` does not exist on first start.** Daemon creates
   it, sets mode 0755, owner ventd:ventd. RULE-STATE-10. No failure.

2. **State file corrupted by power loss during write.** Tempfile +
   rename means we either get the old state (rename didn't complete)
   or the new state (rename completed). Never partial. RULE-STATE-01.

3. **Blob file corrupted by power loss or filesystem damage.** SHA256
   mismatch on read → consumer treats as missing → reinitialises.
   RULE-STATE-02.

4. **Log file torn record from crash mid-append.** Reader detects
   length-overrun or CRC mismatch → skips and continues. Data point
   lost, no crash. RULE-STATE-03 + RULE-STATE-04.

5. **Two ventd processes accidentally launched.** Second process sees
   PID file owned by first, exits with diagnostic. RULE-STATE-06.

6. **User installs newer ventd, reverts to older binary.** Older
   binary sees `version > X` → refuses start with diagnostic
   "newer state file detected, please reinstall newer version or run
   `ventd state reset`." RULE-STATE-05.

7. **AppArmor blocks state directory writes.** Daemon fails fast at
   startup with permission diagnostic + AppArmor log line reference.
   RULE-STATE-09 covers in-process repair of mode bits but cannot
   override AppArmor.

8. **Disk full during state write.** Tempfile write fails before
   rename → old state preserved. Daemon surfaces "out of disk space
   on /var" warning. RULE-STATE-01.

9. **Schema version forward-incompatible (v0.5.0.1 reads state from
   future v0.6.x).** Refuses start. RULE-STATE-05.

10. **Log rotation triggered while writes in flight.** Rename of
    current log to `.1` is atomic; new log file created before next
    append; no append-after-rename window. RULE-STATE-08.

---

## 12. Validation criteria

### 12.1 Synthetic CI tests

Required, all must pass on every PR:

- KV atomic write under simulated power loss (kill mid-write,
  verify rename atomicity).
- Blob SHA256 verification (write blob, corrupt one byte, read,
  verify `found=false`).
- Log torn-record skipping (write 100 records, truncate mid-record-50,
  iterate, verify records 1-49 returned, 50 skipped, 51-100 returned).
- Log rotation under concurrent appends (50 goroutines appending,
  rotate triggered, verify no records lost).
- Schema version forward-rejection (write `version: 99` to sentinel,
  start daemon, verify refusal).
- PID file multi-process detection (start daemon, start second
  daemon, verify second exits).
- Mode-bit repair (chmod state.yaml 0600, restart daemon, verify
  repaired to 0640).

### 12.2 Behavioural HIL

**Fleet member: Proxmox host (192.168.7.10).**

- Multi-restart durability: install ventd, write state via simulated
  consumer, restart daemon 100×, verify state preserved across all
  restarts.
- Power-loss simulation: write state, hard-kill daemon mid-write
  (SIGKILL after fsync of tempfile but before rename), restart,
  verify either old or new state — never partial.

### 12.3 Time-bound metric

**Not applicable** — spec-16 does not affect calibration speed or
controller convergence. Explicit not-applicable declared per
`spec-smart-mode.md` §12.

---

## 13. Estimated cost

- Spec drafting (chat): $0 (this document).
- CC implementation (Sonnet, single tight PR): **$15-25 estimate**.
- Bindings to .claude/rules/: included in PR scope.
- Synthetic CI tests: included in PR scope.
- HIL verification: post-merge, Phoenix manual on Proxmox.

---

## 14. PR sequencing

Single PR. Spec-16 is foundation infrastructure; splitting would
introduce dependency between PRs that ship empty. One PR delivers all
three stores, version sentinel, sysusers integration, AppArmor
updates, RULE-STATE-* bindings, and synthetic tests.

Subsequent patches (v0.5.4 onward) add consumers — those are separate
PRs against the consumer specs, not against spec-16.

---

## 15. Open questions resolved

The deferred draft listed seven design questions. Resolutions:

1. **Single store vs multi-store.** Multi-store. Three shapes covering
   all known consumers. (§3, §4, §5, §6.)
2. **Schema versioning.** Top-level sentinel + per-store versioning.
   Forward migration registry. Downgrade refused. (§7.)
3. **Atomic write semantics.** Tempfile + rename + fsync for KV and
   Blob. `O_APPEND | O_DSYNC` for Log. (§4.2, §5.2, §6.1.)
4. **Lock contention.** Single-process via PID file. Multi-process
   refused. (§4.3, §6 RULE-STATE-06.)
5. **Permissions.** ventd:ventd, 0640 files / 0755 directories,
   sysusers + tmpfiles + AppArmor integration. (§8.)
6. **Migration story.** Forward migration registry per (from, to)
   pair. Most migrations null (additive YAML). (§7.3.)
7. **Test coverage.** §12.1 enumerates synthetic tests covering the
   listed cases.

---

## 16. References

- `spec-smart-mode.md` — design of record for smart-mode architecture.
- `spec-16-persistent-state-DEFERRED.md` — superseded predecessor.
- `specs/spec-06-install-contract.md` — sysusers and AppArmor
  baseline.
- `specs/spec-15-experimental-features.md` §4.1 — F1 toggle-detection
  use case (consumer post-spec-16).
- `specs/spec-05-predictive-thermal.md` — thermal model coefficient
  persistence requirement (consumer post-v0.5.7).
- `specs/spec-13-verification-workflow.md` — telemetry consent
  persistence (consumer post-v0.5.0.1).

---

**End of spec.**
