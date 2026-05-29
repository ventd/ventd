# Shadow mode (#1346)

Shadow mode is a migration on-ramp. An operator already running
CoolerControl / fan2go / thinkfan can start ventd with `apply.shadow:
true` (or the `--shadow` flag) and have it run the FULL smart-mode +
reactive pipeline — calibration learning, decisions, the
recent-decisions feed — while issuing NO hardware writes. The
recent-decisions feed then shows what ventd WOULD do, so the operator can
compare it against their current controller for N days before handing
over fan control.

Shadow mode is a startup-time decision (`--shadow` overlays onto the
loaded config; otherwise `apply.shadow` holds) and never toggles
mid-session. The single source of truth is `Config.Apply.Shadow`, read
via the nil-safe `Config.ShadowMode()` helper.

## RULE-APPLY-SHADOW-01: In shadow mode the daemon issues no hardware fan actuation; every PWM and platform_profile write short-circuits to a "would_write" log line.

The controller's PWM write funnel `writePWMViaPolarity` (reached by all
three write sites via `writeWithRetry`, and the carry-forward retry)
checks `c.cfg.Load().ShadowMode()` FIRST and returns nil without calling
`backend.Write` when shadow mode is on. Because the gate is upstream of
`backend.Write`, no `pwm_enable` mode transition happens either — the
channel is never owned, so the watchdog restore is a natural no-op and
the operator's existing controller stays in charge. The decision and its
observation record were already emitted before the write, so the
recent-decisions feed stays fully populated. The first suppressed write
per controller logs a single operator-visible INFO
(`event=shadow_write_suppressed`); subsequent suppressions log at debug so
journald isn't spammed at poll-rate.

The platform_profile selector is a fan-shaping write surface, so it must
stay silent too: `startPlatformProfileController` injects a `WriteFn`
that logs `event=shadow_platform_profile_suppressed` instead of touching
`/sys/firmware/acpi/platform_profile`. The selector still computes its
choice each tick so the decision remains observable. (`WritePowerProfile`
has no live daemon caller, so there is no third surface to gate today.)

Bound: internal/controller/shadow_test.go:shadow_mode_suppresses_backend_write
Bound: internal/controller/shadow_test.go:shadow_off_writes_through

## RULE-APPLY-SHADOW-02: Calibration is refused (423 Locked) while shadow mode is on.

Calibration cannot run without writing PWM — it sweeps duty and measures
the RPM response — so `handleCalibrateStart` returns
`http.StatusLocked` (423) when `ShadowMode()` is true, short-circuiting
BEFORE the fan lookup so the refusal is unambiguously the shadow gate
rather than a missing-fan error. Silently producing a flat, useless
sweep (every write suppressed) would be worse than refusing. Taking over
for calibration is an explicit, separate step: disable shadow mode and
restart.

Bound: internal/web/shadow_test.go:shadow_on_returns_423
Bound: internal/web/shadow_test.go:shadow_off_does_not_return_423

## RULE-APPLY-SHADOW-03: The status payload carries shadow_mode so the dashboard renders an "observing only" banner.

`/api/v1/status` (and the SSE loop sharing `buildStatus`) sets
`shadow_mode` from the live config. The dashboard polls this and toggles
the blue `dash-shadow-banner` so the operator cannot mistake the live
duty readings — which reflect what ventd WOULD command — for values it is
actually driving.

Bound: internal/web/shadow_test.go:status_reflects_shadow_flag
