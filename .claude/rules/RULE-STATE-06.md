# RULE-STATE-06: Multiple ventd processes against the same state directory MUST be detected via PID file; second process MUST exit with diagnostic.

`AcquirePID(dir)` writes the current process PID to `dir/ventd.pid` and returns
a `release` func that removes it on daemon shutdown. If `ventd.pid` already exists
and contains a PID that responds to `kill(pid, 0)` with no error (i.e. the process
is alive), `AcquirePID` returns `*ErrAlreadyRunning{PID: pid}` immediately without
writing. The caller in `cmd/ventd/main.go` treats this as a fatal startup error,
exiting with a log message that names the conflicting PID. A stale PID file (process
no longer alive) is removed and replaced. This prevents two daemon instances from
racing over the same `state.yaml`, which would produce lost writes under the
tempfile+rename pattern (the last rename wins, discarding intermediate state).

Bound: internal/state/state_test.go:TestRULE_STATE_06_PIDFileMultiProcess
