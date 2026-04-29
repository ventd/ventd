# RULE-IDLE-10: StartupGate returns a non-nil, populated Snapshot on success; snap.Timestamp is non-zero.

`StartupGate(ctx, cfg)` MUST return a non-nil `*Snapshot` as its third return value when
`ok == true`. The returned snapshot represents the system state at the moment the
durability window closed — the last successful capture before the gate unlocked. The
`Snapshot.Timestamp` field MUST be non-zero. This snapshot is passed directly to
`RuntimeCheck` as the baseline so that any process or workload present at calibration-start
is treated as baseline-resident (per RULE-IDLE-07). A nil snapshot returned on success
would force `RuntimeCheck` to reconstruct the baseline from a new capture, losing the
baseline-resident exclusion and potentially refusing immediately on a process that was
already running when the system became idle.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_10_StartupGateReturnsSnapshot
