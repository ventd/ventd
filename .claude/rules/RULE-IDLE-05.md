# RULE-IDLE-05: /proc/loadavg is read via direct file read, not getloadavg(3); no CGO is permitted.

`captureLoadAvg(procRoot string) [3]float64` reads `<procRoot>/loadavg` with `os.ReadFile`
and parses the first three space-separated fields as float64. The package MUST NOT call
`getloadavg(3)` (a libc function) or import any CGO symbol. CGO is incompatible with
`CGO_ENABLED=0`, the project-wide invariant for static binaries. The test verifies that
`captureLoadAvg` returns the correct 1min/5min/15min values from a synthetic
`/proc/loadavg` file, and asserts `PSIAvailable` returns false when `/proc/pressure/cpu`
is absent — confirming the primary/fallback dispatch works without real kernel state.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_05_LoadAvgDirectRead
