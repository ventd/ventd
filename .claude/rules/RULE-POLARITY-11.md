# RULE-POLARITY-11: Every PWM write in the controller hot path MUST route through polarity.WritePWM.

The controller's `writeWithRetry`, its 50ms-retry sub-call, and the
sentinel-carry-forward branch in `tick()` all dispatch backend PWM writes
through `c.writePWMViaPolarity(ch, pwm)` (`internal/controller/controller.go`).
The helper wraps every write in `polarity.WritePWM(c.polarityCh, pwm, fn)` so
inverted-polarity channels receive `255-pwm` (correctly flipped) and phantom /
unknown channels are refused at the polarity helper boundary rather than
silently writing wrong-direction bytes to sysfs.

Pre-#1037 the controller wrote the raw PWM byte direct to `backend.Write` on
all three call sites. `hwdb.InvertPWM` was the only inversion path, and it
read `ChannelCalibration.PolarityInverted` — a field no production code path
ever set to true. The pass-6 audit traced this end-to-end: the wizard
classified inverted-polarity channels correctly (#1026) but the classification
never reached the controller, so on inverted-polarity boards (NCT6683 on MSI,
IT87 on some Gigabyte) ventd asked for slower cooling and the fan went faster.

When the controller has no `polarityCh` wired (nil — test scaffolding before
the channel slice is plumbed), `writePWMViaPolarity` falls back to the
unchanged byte semantics so existing tests that don't supply a channel
continue to pass. A polarity-helper refusal (phantom / unknown) returns nil
from the controller's perspective so the tick loop continues — the refusal
is operator-visible via the WARN log line and the wizard / doctor surface
escalation, not via a tick-level error.

`hwdb.ChannelCalibration.PolarityInverted` is left in place for now as a
parallel system. The audit identified that this bool is read by
`hwdb.InvertPWM` but never written; a follow-up PR will reconcile or delete
it once we confirm no test bindings depend on it. For v0.5.38 the safer
operation is "wire the new system, leave the old in place" so the fix can
ship without a coordinated rule-file rewrite across the catalog.

Bound: internal/controller/safety_test.go:polarity_inverted_routes_via_writepwm
Bound: internal/controller/safety_test.go:polarity_inverted_sentinel_carry_forward_routes_via_writepwm
