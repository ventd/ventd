# RULE-HWDB-CAPTURE-01: Capture writes go to `/var/lib/ventd/profiles-pending/` (or `$XDG_STATE_HOME/ventd/profiles-pending/` in user mode) only.

Capture NEVER writes to the live `profiles.yaml` or `profiles-v1.yaml` at runtime. The
`Capture()` function accepts an explicit `dir` argument and writes only to
`filepath.Join(dir, fingerprint+".yaml")`. In production, `dir` is always the pending
directory returned by `CaptureDir()`, not any path under the embedded catalog filesystem.
A pending profile accumulates until the user explicitly reviews and promotes it; capturing
directly to the live catalog would bypass the review step and could introduce un-verified
board data into the matcher.

Bound: internal/hwdb/capture_test.go:TestRuleHwdbCapture_01_PendingDirOnly
