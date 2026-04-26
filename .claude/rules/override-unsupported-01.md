# RULE-OVERRIDE-UNSUPPORTED-01: Matcher with `overrides.unsupported: true` emits the INFO log exactly once per ventd lifetime per board ID.

When the tier-1 matcher resolves a board profile with `overrides.unsupported: true`, it MUST
call `LogUnsupportedOnce(boardID, log)` which emits exactly one `slog.LevelInfo` message
containing the text `"no Linux fan-control driver"` for that board ID. All subsequent
matches of the same board ID — within the same process lifetime — MUST NOT emit additional
log entries. The once-per-board-ID guarantee is enforced by a package-level `sync.Map` keyed
by board ID; `LoadOrStore` atomically records the first emission and no-ops on all subsequent
calls. This ensures the "sensors-only" message appears exactly once in journald output — not
on every control tick, which would produce log spam at polling_latency_ms_hint frequency.

Bound: internal/hwdb/profile_v1_1_test.go:TestMatcher_UnsupportedEmitsLogOnce
