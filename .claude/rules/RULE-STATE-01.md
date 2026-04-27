# RULE-STATE-01: KV store writes MUST use tempfile + rename + fsync semantics. Direct overwrite is forbidden.

`KVDB.persist()` writes the serialised state to a randomly-suffixed `.tmp.*` file,
calls `fsync` on it, then `os.Rename` to atomically replace the canonical
`state.yaml`. A direct `os.WriteFile` or `os.Create` on `state.yaml` is never
used. The rename is POSIX-atomic on the same filesystem, ensuring that at any
point in time either the old file or the new file is visible — never a partial
write. `BlobDB.Write` and `atomicWrite` use the same pattern for all persistent
files in the state directory.

Bound: internal/state/state_test.go:TestRULE_STATE_01_AtomicWrite
