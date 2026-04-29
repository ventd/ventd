# RULE-IDLE-07: RuntimeCheck computes a delta from the baseline snapshot; baseline-resident blocked processes do not cause refusal.

`RuntimeCheck(ctx context.Context, baseline *Snapshot, cfg GateConfig) (bool, Reason)`
captures a fresh `Snapshot` at call time and compares `snap.Processes` against
`baseline.Processes`. A process name present in `baseline.Processes` is considered
baseline-resident and does NOT trigger a blocked-process refusal, even if the name
appears in the blocklist. Only processes that are NEW since the baseline (present in
the live snapshot but absent from the baseline) cause a refusal. This prevents a
long-running backup job that was already underway when the daemon started from
permanently blocking Envelope C — the baseline records pre-existing activity as
acceptable, and only new activity (user started a new workload) causes a refusal.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_07_RuntimeCheckBaselineDelta
