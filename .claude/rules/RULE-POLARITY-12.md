# RULE-POLARITY-12: On the first polarity refusal per channel lifetime, the controller MUST hand the channel back to BIOS auto via watchdog.RestoreOne; subsequent refusals are silent.

When `polarity.WritePWM` returns `ErrChannelNotControllable` (phantom) or
`ErrPolarityNotResolved` (unknown) inside `writePWMViaPolarity`
(`internal/controller/controller.go`), the controller MUST dispatch
`c.wd.RestoreOne(c.pwmPath)` and emit a single operator-visible WARN
("controller: polarity refused write; handing back to BIOS auto"). The
controller MUST track the handback on a per-instance `polarityHandedBack`
boolean so subsequent refusals within the same controller lifetime are
silent skips — no further WARN emission, no further watchdog dispatch.

Pre-#1110 the controller logged the refusal and returned nil. The fan sat
at whatever PWM the last successful write committed — most commonly the
calibration sweep's final write of PWM=0. For a non-pump fan that's a
loud failure mode; for an AIO pump on an inverted-polarity-misclassified
channel it's a thermal disaster. Closes the 2026-05-15 incident on
Phoenix's 13900K box where every NCT6687-controlled channel sat at
PWM=0 for nearly an hour because the wizard's polarity probe misclassified
fans whose BIOS auto-curve held them at high baseline PWM going into the
midpoint test (the root-cause classification bug is addressed
separately by RULE-POLARITY-13's bipolar probe).

The handback is one-shot per controller. A config reload (SIGHUP) or
daemon restart spawns a fresh controller whose flag starts false again —
re-probe + re-classification on the next wizard run can promote a
previously-refused channel back into the control path without code
changes here. The watchdog's `RestoreOne` is the canonical handback
primitive: it dispatches through `restoreOne` which honours the chip-
specific `pwm_enable` fallback chain (RULE-HWMON-ENABLE-EINVAL-FALLBACK)
and the per-entry panic envelope (RULE-WD-RESTORE-PANIC). A nil
watchdog (test scaffolding) skips the dispatch cleanly.

`writePWMViaPolarity` continues to return nil to the tick loop on a
polarity refusal — the refusal is operator-visible via the WARN line
and the wizard/doctor surface, not via a tick-level error. The handback
is additive: the controller still treats the tick as a skipped write,
the lastPWM state machine is untouched, and the post-handback
`pwm_enable` write is the BIOS auto value (or chip-fallback PWM=255 via
RULE-HWMON-FALLBACK-MISSING-PWMENABLE) — never the daemon's last
committed byte.

Bound: internal/controller/safety_test.go:polarity_refused_phantom_hands_back_to_bios_auto_once
