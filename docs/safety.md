# Safety model

`ventd` controls physical hardware. Losing control of a fan can cook a CPU, VRM, or GPU in seconds. The safety model is defence in depth — multiple independent layers, each of which can hand control back to the BIOS/firmware on its own.

## Exit paths

### Graceful exit

Triggered by `SIGTERM`, `SIGINT`, `context.Context` cancellation, or a panic recovered inside the daemon's deferred `recover()`.

Action: the daemon walks every fan it has touched and restores `pwm_enable` to the value it read at startup. For chips that don't expose `pwm_enable`, PWM is set to `255` (full speed) — the safe fallback for an unknown thermal state.

Latency: milliseconds after the signal arrives.

### Ungraceful exit

Triggered by `SIGKILL`, OOM kill, an unrecovered panic, or a systemd watchdog timeout.

Action: `ventd-recover.service` is a systemd oneshot wired to the main unit via `OnFailure=`. It walks `/sys/class/hwmon/*/pwm<N>_enable` and writes `1` (kernel automatic mode) for every match, returning control to the BIOS firmware curve.

Latency: bounded by `WatchdogSec=2s` on the main unit. If the daemon hangs, systemd kills it within two seconds and the recovery oneshot fires.

### Kernel panic / power loss

No user-space watchdog can cover this. On next boot the firmware regains control and applies its own fan curve, which is the same end state as a graceful exit.

## Calibration sentinel

Calibration sweeps that probe the stop-PWM of a fan deliberately drive PWM to `0`. If a daemon crash or hung sweep causes that state to persist for more than two seconds, a per-fan sentinel escalates PWM to a quiet floor (`30`). A fan can never be left stopped under load by a ventd bug.

## PWM clamping

Every calibration step and every runtime control write is clamped to the fan's configured `[min_pwm, max_pwm]` range. Pump fans have a hard minimum floor enforced before every write; ventd refuses to write below it regardless of curve or manual override.

### Fan-stop gate

`PWM=0` is a special case. Some fans (stock Intel coolers, many case fans) interpret it as "stop"; others treat it as "slowest non-zero duty cycle". To make stop intentional rather than accidental, ventd refuses to write `PWM=0` unless the fan's config has both `min_pwm: 0` *and* `allow_stop: true`. A `min_pwm: 0` without `allow_stop` logs a warning and skips the tick; the fan stays at whatever PWM was last written.

The setup wizard never generates `min_pwm: 0` (the safe default is `min_pwm: 20` or the measured stop-PWM, whichever is higher), so the gate only bites hand-edited YAML. If you want to permit fan stop for a quiet idle, add `allow_stop: true` next to `min_pwm: 0` — the daemon will not stop the fan without it.

## Hardware change detection

A new fan or GPU plugged in mid-run does not bypass safety. `ventd` notices the uevent within a second (or within ten seconds via periodic rescan when `AF_NETLINK` is unavailable), enumerates the new controls read-only, and waits for the operator to accept them in the UI before any write.

## Exotic hardware

If you run server chassis, custom-loop AIOs, or unusual Super I/O chips, validate calibration results before leaving the daemon unattended. Every exit path restores `pwm_enable=1`, so the worst case of a bad calibration is BIOS-curve fallback on next restart — but BIOS curves on exotic boards are not always conservative.

## Reporting safety issues

A fan left in an unsafe state by `ventd` is a security issue, not a regular bug. Report via [SECURITY.md](../SECURITY.md), not the public issue tracker.
