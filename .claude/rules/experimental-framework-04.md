# RULE-EXPERIMENTAL-STARTUP-LOG-ONCE: LogActiveFlagsOnce emits at most one INFO log per 24h; no log when no flags are active.

`experimental.LogActiveFlagsOnce(flags Flags, statePath string, logger *slog.Logger, now func() time.Time)`
MUST:
- Emit nothing and create no state file when `flags.Active()` is empty.
- On first call (no state file), emit one `slog.LevelInfo` log listing active flags and write the
  current timestamp (RFC3339) to `statePath`.
- Suppress the log when the state file records a timestamp within the last 24h (suppression window).
- Re-emit the log and update the state file when the state file timestamp is older than 24h.

The test fixtures cover: first-run emission, within-window silence, after-window re-emission, and
zero-flag silence. The `now` parameter is injected for deterministic time control in tests.

Bound: internal/experimental/startup_log_test.go:TestStartupLog_FirstRunEmits
