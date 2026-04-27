# Persistent State — `internal/state`

ventd v0.5.1 introduces a structured persistent state layer under
`/var/lib/ventd/`. This document describes the design of that layer, the
three storage primitives it provides, and the invariants that govern them.

## Directory layout

```
/var/lib/ventd/
  version          # schema version sentinel (plain integer)
  ventd.pid        # PID file for multi-process detection
  state.yaml       # KV store
  models/          # binary blob store (RLS weights, etc.)
    *.blob
  logs/            # append-only event logs
    *.log
    *.log.1
    *.log.1.gz
```

Ownership: `ventd:ventd`. Directories are mode `0755`; files are mode `0640`.
The `deploy/tmpfiles.d-ventd.conf` fragment ensures systemd-tmpfiles creates
the hierarchy on boot before the daemon starts.

## Primitives

### KV store (`KVDB`)

`state.yaml` is a flat YAML file with a `schema_version: 1` header and an
arbitrary namespace → key → value structure. It is loaded entirely into
memory at startup and written atomically on each mutation.

**Write path**: tempfile + `fsync` + `os.Rename` (POSIX-atomic on the same
filesystem). Direct `WriteFile` on the canonical path is never used; a crash
mid-write leaves a `.tmp.*` orphan that is ignored on the next open (RULE-STATE-01).

**Concurrency**: an `RWMutex` protects in-memory state. Reads (`Get`, `List`)
take a read lock; writes (`Set`, `Delete`) take the write lock and call
`persist()` before releasing.

**Transactions**: `WithTransaction(fn)` deep-copies the in-memory map into a
`KVTx` snapshot, calls `fn(tx)`, and — only if `fn` returns nil — replaces
the live map and calls `persist()` once. A non-nil return from `fn` leaves
the map unchanged and never calls `persist()` (RULE-STATE-07).

### Blob store (`BlobDB`)

Each blob is a file under `models/` with a 32-byte header:

```
[0:4]   magic       "VBLB" (0x56 0x42 0x4C 0x42)
[4:6]   schema_ver  uint16 little-endian
[6:8]   reserved    0x0000
[8:16]  length      uint64 little-endian (payload byte count)
[16:48] SHA256      sha256.Sum256(payload)
[48:]   payload     opaque bytes
```

**Read**: verifies magic, length, and SHA256. Any mismatch returns
`(nil, 0, false, nil)` — the caller re-initialises from scratch (RULE-STATE-02).

**Write**: assembles header + payload, calls `atomicWrite` (same tempfile +
rename pattern as the KV store).

### Append-only log store (`LogDB`)

Each named log is an `O_APPEND | O_DSYNC` file under `logs/`. `O_APPEND`
makes each `write(2)` seek-to-EOF atomically; `O_DSYNC` makes each write
durable before returning (RULE-STATE-03).

**Record format**: `uint32 length | payload | uint32 CRC32-IEEE(length || payload)`.
A torn record (length overrun or `io.ErrUnexpectedEOF`) stops iteration for
that file; a CRC mismatch skips the record and continues (RULE-STATE-04).

**Iteration**: collects rotated files oldest-first, then the current file,
and calls a visitor for each valid record. The `since time.Time` parameter
skips files whose last-modified time is before `since`.

**Rotation** (`rotateLocked`): closes the current handle, shifts
`.keepCount` → delete, `.N-1` → `.N` … `.1` → `.2`, renames current →
`.1`, then opens a fresh current file. Optionally gzip-compresses `.1` in
the background when it exceeds 10 MiB. The entire sequence is protected by
`h.mu` so no append can race across the rename (RULE-STATE-08).

## Schema version sentinel

`/var/lib/ventd/version` holds a plain ASCII integer (the current schema
version is `1`).

`state.Open` calls `CheckVersion` which:

- **Missing**: writes `1`, proceeds.
- **Match**: proceeds.
- **on-disk > current**: returns `ErrDowngrade` — the daemon refuses to
  start when it encounters state written by a future binary (RULE-STATE-05).
- **on-disk < current**: runs registered `MigrateFn` chain, updates sentinel.

Migration functions are registered with `RegisterMigration(from, to, fn)`.
If no function is registered for a step the state is treated as missing.

## Multi-process detection

`AcquirePID(dir)` reads `dir/ventd.pid`, checks whether that PID is alive via
`kill(pid, 0)`, and returns `*ErrAlreadyRunning` if it is. A stale PID file
(process dead) is removed and replaced with the current PID. The returned
`release` function removes the file on clean daemon exit (RULE-STATE-06).

## File mode invariants

`atomicWrite` always creates files with mode `0640`. `initDirs` creates
directories with mode `0755`. If `state.yaml` is found with the wrong mode
at open time, `repairMode` applies `os.Chmod` and logs a warning — it does
not refuse to load (RULE-STATE-09).

## Relationship to other subsystems

| Subsystem | Expected use |
|-----------|-------------|
| Calibration (spec-04) | Blob store for RLS weight vectors |
| Predictive thermal (spec-05) | KV store for setpoint history |
| Diagnostic bundle (spec-10c) | Log iteration for structured event export |
| Doctor / hwdiag | KV store for persistent advisory cache |

None of these consumers are wired in this PR; the foundation is infrastructure
only. The `_ = st` placeholder in `cmd/ventd/main.go` will be replaced as
each consumer lands.

## Testing

All invariants are covered by `internal/state/state_test.go` with the race
detector enabled (`go test -race`). Tests are hermetic: they create temp
directories via `t.TempDir()` and never touch `/var/lib/ventd`.

Rule bindings enforced by `tools/rulelint`: RULE-STATE-01 through
RULE-STATE-10.
