# RULE-STATE-03: Log store appends MUST use `O_APPEND | O_DSYNC`. Buffered writes are forbidden for log primitive.

`logHandle.openFileLocked()` MUST open the log file with flags
`os.O_WRONLY | os.O_CREATE | os.O_APPEND | syscall.O_DSYNC`. `O_APPEND` makes
each `write(2)` syscall seek to the current end-of-file atomically, preventing
interleaved records from concurrent writers. `O_DSYNC` ensures that data is
durable on the storage medium before the syscall returns — a crash after a
successful `Write` call will not lose that record. Buffered writes (e.g.
`bufio.Writer`) introduce a window where records are in kernel or userspace
buffers but not yet durable; this is forbidden for the log store. Static
analysis of `log.go` must confirm both flags are present.

Bound: internal/state/state_test.go:TestRULE_STATE_03_LogOAppendODsync
