# RULE-ENVELOPE-12: A channel in state "paused_*" re-runs the idle.StartupGate before resuming the probe.

When the daemon restarts and finds a channel KV with `state` in {"paused_user_idle",
"paused_thermal", "paused_load"}, `Prober.Probe` MUST call `idle.StartupGate` again before
resuming. The gate enforces that the pause condition has cleared before committing to the
next step. If `StartupGate` returns `ok=false`, the channel is re-paused (KV state updated)
and the probe for that channel stops. If `ok=true`, the probe resumes from
`completed_step_count`. The test injects a paused channel and a mock `StartupGate` that
returns ok=false on the first call and ok=true on the second, then verifies the probe
does not write on the first daemon start but does resume on the second.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_12_PausedStateReruns_StartupGate
