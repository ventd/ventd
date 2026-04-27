# RULE-PROBE-08: Daemon start consults wizard.initial_outcome KV key; LoadWizardOutcome returns the correct Outcome enum value.

`LoadWizardOutcome(db *state.KVDB) (Outcome, bool, error)` reads `wizard.initial_outcome`
and maps its string value to the `Outcome` enum: `"control_mode"` → `OutcomeControl`;
`"monitor_only"` → `OutcomeMonitorOnly`; `"refused"` → `OutcomeRefuse`. When the key is
absent, it returns `(OutcomeControl, false, nil)` — a missing key means "never probed",
not "refused". The daemon startup path calls `LoadWizardOutcome` after `state.Open` and
before starting the control loop, gating entry to full control mode on `OutcomeControl`
and refusing start on `OutcomeRefuse`.

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-08_load_wizard_outcome
