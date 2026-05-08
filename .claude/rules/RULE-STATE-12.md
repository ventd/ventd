# RULE-STATE-12: KV writes refuse before mutating in-memory state when the state directory has less than iox.MinFreeBytesForState bytes free.

`KVDB.Set`, `KVDB.Delete`, and `KVDB.WithTransaction` all call
`ensureFreeSpaceFn(filepath.Dir(db.path))` BEFORE acquiring `db.mu` and
BEFORE mutating `db.data`. Production points the seam at
`iox.EnsureFreeSpace(_, iox.MinFreeBytesForState)` (1 MiB threshold).

The pre-flight gate is structurally distinct from the persist-time
write: it can be tightened, loosened, or stubbed in tests via the
`ensureFreeSpaceFn` package-level seam. The seam exists so unit tests
can exercise the refusal path without requiring an actually-low-space
filesystem; production code never reassigns it.

The pre-flight position (before mutex, before mutation) is load-bearing.
Prior to this rule, `Set` mutated `db.data[ns][key] = value` first
(`kv.go:100`) and called `persist()` second (`kv.go:101`); a `persist`
failure on ENOSPC returned the error to the caller but left the in-
memory map advanced. On daemon restart, `load()` read the OLD on-disk
value while the runtime had been quietly running on the NEW value —
the silent in-memory/on-disk divergence the senior-review's C7 finding
identified for `wizard.initial_outcome`, calibration state, polarity
records, and the smart-mode shard root.

The refusal preserves the existing transactional contracts:

- `Set` and `Delete` return the wrapped `iox.ErrInsufficientFreeSpace`
  error; subsequent `Get` returns the original (un-mutated) value.
- `WithTransaction` refuses BEFORE invoking the caller's `fn` closure,
  so an operator that does expensive work inside `fn` doesn't burn
  the cycles only to discover the commit can't land. RULE-STATE-07's
  "fn-returns-error leaves the world untouched" extends to "low-disk
  returns iox.ErrInsufficientFreeSpace before fn ever runs".
- The on-disk `state.yaml` is unchanged on the refusal path (no
  `atomicWrite` is attempted).

The threshold is shared across all three call paths so a single
operator-tunable knob (future v0.6.0 `state.yaml` directive) can
adjust the entire KV's refusal behaviour without per-call overrides.
TOCTOU between the pre-flight statfs and the eventual `atomicWrite`
is acceptable: the gate catches the common case (disk has been
near-full for a while) rather than racing against pathological
"another process filled the disk in 50µs" scenarios — that case is
caught by `atomicWrite`'s own ENOSPC error return on the actual
write, which Set/Delete/WithTransaction propagate verbatim to the
caller.

Bound: internal/state/state_test.go:TestRULE_STATE_12_FreeSpaceGuard/RULE-STATE-12_set_refuses_before_in_memory_mutation
Bound: internal/state/state_test.go:TestRULE_STATE_12_FreeSpaceGuard/RULE-STATE-12_delete_refuses_before_in_memory_mutation
Bound: internal/state/state_test.go:TestRULE_STATE_12_FreeSpaceGuard/RULE-STATE-12_with_transaction_refuses_before_calling_fn
Bound: internal/state/state_test.go:TestRULE_STATE_12_FreeSpaceGuard/RULE-STATE-12_seam_restored_after_test_lets_subsequent_writes_pass
