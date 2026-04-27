# RULE-STATE-07: KV `WithTransaction` MUST serialise to a single atomic write at commit. Partial commits across failure are forbidden.

`KVDB.WithTransaction(fn)` deep-copies the current in-memory state into a
`KVTx` snapshot, calls `fn(tx)`, and — only if `fn` returns nil — replaces
`db.data` with `tx.data` and calls `db.persist()` once. If `fn` returns a
non-nil error, `db.data` is left unchanged and `persist()` is never called.
The combination of a single `persist()` call and the `atomicWrite` helper
(tempfile+rename) ensures that either the pre-transaction state or the
fully-committed post-transaction state is on disk — never a partial state
containing some-but-not-all of the transaction's mutations. Tests verify both
the success path (all keys visible after commit) and the rollback path (no
keys visible after failure).

Bound: internal/state/state_test.go:TestRULE_STATE_07_TransactionAtomicCommit
