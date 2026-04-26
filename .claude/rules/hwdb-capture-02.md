# RULE-HWDB-CAPTURE-02: Capture cannot run if the anonymiser fails. The capture function returns an error and writes nothing — fail closed.

`Capture()` calls `callAnonymise(profile)` before any write attempt. If
`callAnonymise` returns a non-nil error, `Capture` returns that error immediately
and no file is created in the pending directory. The test verifies this by injecting a
failing anonymiser via `atomicAnonymiseFn` and asserting that `Capture` returns a
non-nil error AND that the temporary directory remains empty. Fail-closed semantics
ensure that a broken anonymiser never produces a bundle with un-stripped PII: the
correct response is always "abort capture" rather than "write and hope."

Bound: internal/hwdb/capture_test.go:TestRuleHwdbCapture_02_FailClosedOnAnonymise
