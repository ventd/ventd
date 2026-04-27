# RULE-STATE-08: Log rotation MUST NOT lose in-flight records. Atomic rename + new file creation, no append-after-rename window.

`logHandle.rotateLocked()` executes the following sequence while holding `h.mu`:

1. Close the current file handle (no further writes possible).
2. Shift existing rotated files: `.keepCount` deleted, `.N-1` → `.N`, …, `.1` → `.2`.
3. `os.Rename(logPath, logPath+".1")` — atomic POSIX rename.
4. `h.openFileLocked()` — create and open a new `logPath` for future appends.
5. (Optional, background) gzip-compress `.1` if size > 10 MiB.

Because `h.mu` is held for the entire sequence, no `Append` call can write to
the old file after step 1, and no `Append` can write to the new file before step 4.
This eliminates the append-after-rename window where a record written to the old
file path after the rename would appear in `.1` without the caller knowing.
`Iterate` collects both the current file and all rotated files, so all records
written before and after a rotation are visible during iteration.

Bound: internal/state/state_test.go:TestRULE_STATE_08_LogRotationNoRecordLoss
