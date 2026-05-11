# Pass 3 — Rule↔Binding Integrity (RULE-POLARITY-* sub-pass)

**Date**: 2026-05-11
**Audit**: Comprehensive ghost-code sweep, third pass
**Baseline commit**: `e49ac20`
**Scope**: every `RULE-POLARITY-*` rule across `.claude/rules/RULE-POLARITY-*.md` — 11 rules.

## Findings

### SOLID (9/11)

| rule | claim | exercise |
|---|---|---|
| RULE-POLARITY-01 | midpoint write = 128 (hwmon) / 50 (NVML) | inspects recorded write byte after `ProbeChannel`; both backends covered |
| RULE-POLARITY-02 | hold time 3 s ± 200 ms | `clockFn` accumulator verifies `totalSleep ≥ HoldDuration - 200ms` |
| RULE-POLARITY-03 | classification thresholds 150 RPM / 10 % | table-driven: +620 normal, -620 inverted, +149/-149/0 phantom — directly tests the boundary in both directions |
| RULE-POLARITY-04 | baseline restored on every exit path | dedicated subtests for write-fail + ctx-cancel; both verify the deferred restore fires |
| RULE-POLARITY-05 | `WritePWM` refuses phantom/unknown; rewrites normal/inverted | exhaustive — phantom → `ErrChannelNotControllable`; unknown → `ErrPolarityNotResolved`; normal verbatim; inverted = 255−value |
| RULE-POLARITY-06 | NVML driver < R515 returns phantom/driver_too_old | uses `fixtures.NewFakeNVML("510.108.03", …)`, verifies phantom result AND `SetSpeedCalls() == 0` (no write attempted) |
| RULE-POLARITY-08 | `ApplyOnStart` matches persisted results by PWMPath | persist → ApplyOnStart → verify match + miss handling |
| RULE-POLARITY-09 | reset wipes calibration namespace | persist → wipe → verify empty |
| RULE-POLARITY-10 | every phantom reason code refused by `WritePWM` | iterates all 6 `PhantomReason*` constants, asserts each returns `ErrChannelNotControllable` and `IsControllable` false |

### BORDERLINE (1/11)

| rule | gap |
|---|---|
| RULE-POLARITY-11 | Rule promises **three** controller hot-path call sites route through `polarity.WritePWM`: (1) `writeWithRetry`'s main write, (2) the 50ms-retry sub-call inside `writeWithRetry`, (3) the sentinel-carry-forward branch in `tick()`. The bound test `polarity_inverted_routes_via_writepwm` drives `writeWithRetry` directly — which transitively covers (1) AND (2) because both call the same `writePWMViaPolarity` helper. **Call site (3) — the `tick()` sentinel-carry-forward branch — is not exercised with a polarityCh wired.** The test's own header note acknowledges this: *"the sentinel branch is exercised indirectly via the rules in the existing sentinel/* subtests above."* Those existing sentinel/* subtests construct controllers without setting `c.polarityCh`, so they verify the carry-forward HAPPENS but not that it routes through polarity. A regression that deleted the `writePWMViaPolarity(ch, c.lastPWM)` call on the sentinel branch (`controller.go:650`) and replaced it with a raw `backend.Write` would silently pass CI on inverted-polarity boards — exactly the regression class that bit Phoenix's hosts pre-#1067. |

### GHOST (already filed)

| rule | status |
|---|---|
| RULE-POLARITY-07 | IPMIVendorProbe interface is declared, three vendor impls exist, but the interface has zero production dispatch sites. The bound subtest constructs each impl directly and verifies the per-vendor probe behaviour — but the rule's implicit promise that the interface is **used** by the wizard / probe path is not enforced. Pass-2 caught this and filed as #1071. No re-file. |

## Running tally

| sub-pass | total | SOLID | BORDERLINE | WEAK | GHOST (filed) |
|---|---|---|---|---|---|
| smart-mode-wiring | 5 | 2 | 0 | 3 | 0 |
| RULE-CPL-* | 15 | 13 | 2 | 0 | 0 |
| RULE-CMB-* | 26 | 22 | 4 | 0 | 0 |
| RULE-POLARITY-* | 11 | 9 | 1 | 0 | 1 (Pass-2 #1071) |
| **total** | **57** | **46** | **7** | **3** | **1** |

## Filing

**RULE-POLARITY-11 sentinel-carry-forward** — fileable as a new follow-up. The fix is one extra subtest under `TestRulePolarity11_ControllerHotPathRoutesViaPolarityWritePWM`: construct a controller with `polarityCh = &probe.ControllableChannel{Polarity: "inverted"}`, drive a sentinel tick that exercises the carry-forward branch (seed lastPWM at tick 1, inject sentinel at tick 2), and assert the sysfs file contains the inverted byte (`255 - lastPWM`) rather than `lastPWM` raw. ~20 LOC.

## Next

Pass 3 continues with **RULE-WD-*** (watchdog) — 9 rules, recently changed by #1070 (per-syscall deadlines + IPMI routing). Per the heuristic, the goroutine-bounded budget tests (RULE-WD-RESTORE-BUDGET) and the per-channel restore claims (RULE-WD-RESTORE-EXIT) are the highest-stakes wirings to sample first.
