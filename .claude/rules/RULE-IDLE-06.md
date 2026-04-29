# RULE-IDLE-06: Process blocklist includes canonical R5 §7.1 entries and is extensible via SetExtraBlocklist.

`isBlockedProcess(name string) bool` returns true for every process name in the base
blocklist defined in §7.1 of spec-v0_5_3: `rsync`, `restic`, `borg`, `ffmpeg`, `apt`,
`dnf`, and equivalent backup/transcoding/package-manager names. `SetExtraBlocklist(names []string)`
appends operator-specified names to the base list; the extension takes effect for all
subsequent `isBlockedProcess` calls within the process lifetime. A `nil` argument resets
the extra list. The test verifies that all canonical §7.1 names are blocked, that a
custom name added via `SetExtraBlocklist` is blocked, and that an unrelated process name
is not blocked. The blocklist gate prevents Envelope C from starting while a backup or
encode job is running in the background.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_06_ProcessBlocklist
