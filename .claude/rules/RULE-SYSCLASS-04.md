# RULE-SYSCLASS-04: Ambient reading outside [10, 50]°C is rejected as implausible before Envelope C starts.

`AmbientBoundsOK(reading float64) (code string, ok bool)` MUST return `("", true)` when
`reading` is in the closed interval [10.0, 50.0]°C. When `reading < 10.0`, it returns
`("AMBIENT-IMPLAUSIBLE-TOO-COLD", false)`. When `reading > 50.0`, it returns
`("AMBIENT-IMPLAUSIBLE-TOO-HOT", false)`. The Envelope C orchestrator calls
`AmbientBoundsOK` on the value returned by `identifyAmbient` before parameterising the
thermal curve; an ambient outside this range indicates a sensor wiring error, a sentinel
leak, or test-fixture pollution. Running Envelope C on a garbage ambient would produce a
curve anchored at a physically impossible temperature and silently mis-calibrate every fan
on the system.

Bound: internal/sysclass/sysclass_test.go:TestRULE_SYSCLASS_04_AmbientBoundsRefusal
