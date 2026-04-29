# RULE-IDLE-01: StartupGate requires the idle predicate to be TRUE for ≥ 300 s (durability window) before returning ok=true.

`StartupGate(ctx context.Context, cfg GateConfig) (ok bool, reason Reason, snap *Snapshot)`
polls the idle predicate at `cfg.TickInterval` intervals. It tracks a consecutive-true
duration and only returns `(true, ReasonOK, snap)` once that duration reaches
`cfg.Durability` (default 300 s). A single true tick does not satisfy the gate — the idle
state must be *sustained* to ensure the system is genuinely quiescent rather than between
bursts of activity. On context cancellation before the durability window is met, the gate
returns `(false, reason, nil)`. A false tick resets the consecutive-true counter to zero.
Skipping durability and returning on the first true tick would allow a calibration to start
during a workload pause, producing incorrect fan curves.

Bound: internal/idle/idle_test.go:TestRULE_IDLE_01_StartupGate_DurabilityRequired
