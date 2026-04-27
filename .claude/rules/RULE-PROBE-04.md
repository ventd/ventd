# RULE-PROBE-04: ClassifyOutcome follows the §3.2 algorithm exactly — virt/container → refuse; no sensors → refuse; sensors only → monitor_only; sensors + channels → control.

`ClassifyOutcome(r *ProbeResult) Outcome` applies four rules in priority order:
1. `Virtualised || Containerised` → `OutcomeRefuse` ("refused").
2. `len(ThermalSources) == 0` → `OutcomeRefuse`.
3. `len(ControllableChannels) == 0` → `OutcomeMonitorOnly` ("monitor_only").
4. Otherwise → `OutcomeControl` ("control_mode").

The function is pure and does not read `CatalogMatch`. The three-state outcome drives the
setup wizard fork: refuse aborts the install flow, monitor_only enters a read-only dashboard,
and control enters the full calibration pipeline.

Bound: internal/probe/probe_test.go:TestProbe_Rules/RULE-PROBE-04_classify_outcome
