# Mass-stall tracker rules — R11

These invariants govern `internal/massstall`, the system-wide
concurrent-stall tracker that backs the `w_pred_system` gate's
no-mass-stall term (spec-v0_5_9 §2.5). A single dead or operator-parked
fan is normal and must not disable predictive control; a *cluster* of
fans commanded to spin but reading zero RPM is the signature of a
system-level cooling fault. Controllers report one
`(channel, committed PWM, observed RPM)` per committed tick via the
controller's `WithStallReporter` option; the gate evaluator reads
`MassStalled`.

## RULE-MASSTALL-TRIP-01: MassStalled trips only when at least MinChannels distinct channels are stalled within Window.

The default `MinChannels` is 2 — the smallest count that distinguishes a
single dead / AllowStop fan (legitimate, never trips) from a system-level
failure. `Snapshot` reports the count + sorted IDs of currently-stalled
channels for the doctor/API surface.

Bound: internal/massstall/massstall_test.go:TestMassStall_TripsAtThreshold
Bound: internal/controller/multi_fan_test.go:TestMultiFan_MassStallTripsAtThresholdEndToEnd

## RULE-MASSTALL-FLOOR-01: a stall requires commandedPWM >= StallPWMFloor AND observedRPM == 0; a tach-less read (-1) never counts.

`StallPWMFloor` mirrors the controller's stuck-fan stiction floor (77 =
30% of 255): a fan commanded below it that reads zero is working as
intended. A tach-less or failed read (`observedRPM == -1`) cannot be
distinguished from a healthy fan, so it is never counted as a stall.

Bound: internal/massstall/massstall_test.go:TestMassStall_FloorAndTachless

## RULE-MASSTALL-WINDOW-01: a non-stall report clears a channel immediately; a channel that stops reporting expires after Window.

`Report` stores the most recent stall timestamp per channel and clears
the entry on any non-stall report (recovery, or dropping below the
floor). A channel that stops reporting entirely (e.g. its controller
goroutine exited while stalled) expires from the count after `Window` so
a phantom stall cannot linger forever. The tracker answers "are
>= MinChannels fans stalled right now", not "did a stall ever happen".

Bound: internal/massstall/massstall_test.go:TestMassStall_RecoveryAndExpiry

## RULE-CTRL-STALL-REPORT-01: WithStallReporter feeds (channel, committed PWM, observed RPM) on every committed tick, reusing the stuck-fan tach read; nil-safe.

The controller calls the stall reporter from `markTickCompleted` with
the byte just written and the RPM that `maybeWarnStuckFan` already read,
so no extra sysfs read is added on the hot path. The fire-once stuck-fan
WARN gate suppresses only the repeat log line, not the read, so an
already-warned channel keeps reporting its current RPM. A controller
without a reporter completes ticks unchanged (pre-R11 behaviour).

Bound: internal/controller/controller_test.go:TestController_StallReporterFires
Bound: internal/controller/stall_detection_e2e_test.go:TestTick_StallDetectedThroughRealBackendFlipsMassStall
