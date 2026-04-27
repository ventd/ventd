# RULE-STATE-09: All state files MUST be created with mode `0640 ventd ventd`; directories `0755 ventd ventd`. Mode mismatches on read MUST be repaired (not refused) to handle umask quirks during install.

`atomicWrite` creates files with mode `0640` (`fileMode`). `initDirs` creates
directories with mode `0755` (`dirMode`). If `state.yaml` already exists on disk
with a different mode (e.g. `0600` from a restrictive umask), `openKV` calls
`repairMode()` which detects the mismatch via `os.Stat` and applies
`os.Chmod(path, 0640)` before loading the file. The daemon logs a warning but
continues normally — refusing to start because of a mode mismatch would break
systems where the installer or sysadmin created the file with a different umask.
The repair guarantees that the diag bundle process (which reads via group
membership) can access state files after the first daemon restart.

Bound: internal/state/state_test.go:TestRULE_STATE_09_FileModeRepair
