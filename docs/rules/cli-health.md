# CLI: health probe

## RULE-CLI-HEALTH: `ventd health` surfaces the last-startup-fatal sentinel out-of-process.

When the daemon repeatedly fails to start, systemd eventually gives up and the
web/HTTP surface never binds — so any in-daemon health endpoint is unreachable
exactly when it would be useful. ventd already records a one-line summary of a
fatal startup exit to the `last-fatal.txt` sentinel (`internal/lastfatal`,
written on the fatal-exit path, cleared once startup progresses past the common
fatal modes). The `ventd health` subcommand is the out-of-process reader of that
sentinel: a separate, short-lived process that works whether or not the daemon
is up.

`runHealth(dir, stdout)` reads the sentinel (`lastfatal.Read`) and the pidfile
(`state.RunningPID` — a read-only probe that never creates, rewrites, or removes
the file, so it is safe to run alongside a live daemon). Verdict precedence: a
recorded startup fatal is the headline and exits non-zero (`healthExitFatal`),
even when a daemon is now running, because it means the *last* start failed and
the operator should see why; otherwise it reports pidfile liveness and exits
zero. The state directory is resolved via `state.EffectiveDir()` so
`VENTD_STATE_DIR` is honoured (matching where `lastfatal.Write`/`Clear` and the
daemon's pidfile live).

Bound: cmd/ventd/health_test.go:TestRunHealth
Bound: internal/state/runningpid_test.go:TestRunningPID
